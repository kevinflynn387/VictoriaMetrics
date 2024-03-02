package streamaggr

import (
	"strings"
	"sync"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/fasttime"
)

// uniqueSamplesAggrState calculates output=unique_samples, e.g. the number of unique sample values.
type uniqueSamplesAggrState struct {
	m sync.Map
}

type uniqueSamplesStateValue struct {
	mu      sync.Mutex
	m       map[float64]struct{}
	deleted bool
}

func newUniqueSamplesAggrState() *uniqueSamplesAggrState {
	return &uniqueSamplesAggrState{}
}

func (as *uniqueSamplesAggrState) pushSamples(samples []pushSample) {
	for i := range samples {
		s := &samples[i]
		outputKey := getOutputKey(s.key)

	again:
		v, ok := as.m.Load(outputKey)
		if !ok {
			// The entry is missing in the map. Try creating it.
			v = &uniqueSamplesStateValue{
				m: map[float64]struct{}{
					s.value: {},
				},
			}
			outputKey = strings.Clone(outputKey)
			vNew, loaded := as.m.LoadOrStore(outputKey, v)
			if !loaded {
				// The new entry has been successfully created.
				continue
			}
			// Use the entry created by a concurrent goroutine.
			v = vNew
		}
		sv := v.(*uniqueSamplesStateValue)
		sv.mu.Lock()
		deleted := sv.deleted
		if !deleted {
			if _, ok := sv.m[s.value]; !ok {
				sv.m[s.value] = struct{}{}
			}
		}
		sv.mu.Unlock()
		if deleted {
			// The entry has been deleted by the concurrent call to appendSeriesForFlush
			// Try obtaining and updating the entry again.
			goto again
		}
	}
}

func (as *uniqueSamplesAggrState) appendSeriesForFlush(ctx *flushCtx) {
	currentTimeMsec := int64(fasttime.UnixTimestamp()) * 1000
	m := &as.m
	m.Range(func(k, v interface{}) bool {
		// Atomically delete the entry from the map, so new entry is created for the next flush.
		m.Delete(k)

		sv := v.(*uniqueSamplesStateValue)
		sv.mu.Lock()
		n := len(sv.m)
		// Mark the entry as deleted, so it won't be updated anymore by concurrent pushSample() calls.
		sv.deleted = true
		sv.mu.Unlock()
		key := k.(string)
		ctx.appendSeries(key, "unique_samples", currentTimeMsec, float64(n))
		return true
	})
}