package pressure

import (
	"log/slog"
	"sync"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
	"github.com/Chriszly/usage-control/backend/internal/pdh"
)

// The counters, by their English names, which work in every language of
// Windows.
const (
	processorQueuePath = `\System\Processor Queue Length`
	pagesInputPath     = `\Memory\Pages Input/sec`
	diskIdlePath       = `\PhysicalDisk(_Total)\% Idle Time`
)

// reader reads the performance counters that Windows carries; any account
// may read them.
type reader struct {
	mu                                   sync.Mutex
	query                                *pdh.Query
	processorQueue, pagesInput, diskIdle pdh.Counter
}

// NewReader returns what reads the closest signals to pressure that Windows
// offers (see counters). When the counters cannot be opened it logs why and
// reports nothing.
func NewReader() func() []metrics.Extra {
	query, err := pdh.Open()
	if err != nil {
		slog.Error("the performance counters could not be opened", "error", err)
		return func() []metrics.Extra { return nil }
	}
	r := &reader{query: query}
	// A counter that cannot be added, such as the disks' on a machine without
	// the disk counters, is left out.
	for _, c := range []struct {
		path    string
		counter *pdh.Counter
	}{
		{processorQueuePath, &r.processorQueue},
		{pagesInputPath, &r.pagesInput},
		{diskIdlePath, &r.diskIdle},
	} {
		counter, err := query.Add(c.path)
		if err != nil {
			slog.Warn("a performance counter is missing", "error", err)
			continue
		}
		*c.counter = counter
	}
	// Rates are measured between two readings, so the first one starts them,
	// a second ahead of the first read: over a shorter time, one page read
	// from disk would show as thousands per second. A failed start shows as
	// missing values.
	_ = query.Collect()
	time.Sleep(time.Second)
	return r.read
}

func (r *reader) read() []metrics.Extra {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.query.Collect(); err != nil {
		return nil
	}
	return fromCounters(counters{
		processorQueue: counterValue(r.processorQueue),
		pagesInput:     counterValue(r.pagesInput),
		diskIdle:       counterValue(r.diskIdle),
	})
}

// counterValue returns the value of a counter of a single instance, or nil when it
// has none, such as a counter that was not added.
func counterValue(counter pdh.Counter) *float64 {
	if counter == 0 {
		return nil
	}
	values, err := counter.Values()
	if err != nil {
		return nil
	}
	return single(values)
}
