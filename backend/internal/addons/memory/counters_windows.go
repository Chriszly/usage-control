package memory

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
	"github.com/Chriszly/usage-control/backend/internal/pdh"
)

// On Windows the memory details come from the performance counters of the
// Memory object, the ones Performance Monitor shows.

// counterReader reads the counters of one query.
type counterReader struct {
	query    *pdh.Query
	counters map[string]pdh.Counter // by counter path
	// read is whether the query was read before, which the rates need.
	read bool
}

// New returns what reads the memory details of this machine: the Memory
// performance counters, or nothing where they cannot be opened.
func New() func(now time.Time) []metrics.Extra {
	r, err := openCounters()
	if err != nil {
		slog.Warn("the memory counters cannot be read", "error", err)
		return func(time.Time) []metrics.Extra { return nil }
	}
	return r.Read
}

// openCounters opens a query with every counter Windows has; one it lacks
// is left out.
func openCounters() (*counterReader, error) {
	query, err := pdh.Open()
	if err != nil {
		return nil, err
	}
	r := &counterReader{query: query, counters: map[string]pdh.Counter{}}
	for _, c := range counters {
		if counter, err := query.Add(c.path); err == nil {
			r.counters[c.path] = counter
		}
	}
	if len(r.counters) == 0 {
		query.Close()
		return nil, fmt.Errorf("no counter of the Memory object")
	}
	return r, nil
}

// Read returns the memory details as the group of extras the collector
// shows. Windows measures the rates between two reads, so the first read
// leaves them out.
func (r *counterReader) Read(time.Time) []metrics.Extra {
	if err := r.query.Collect(); err != nil {
		return nil
	}
	values := make(map[string]float64, len(r.counters))
	for path, counter := range r.counters {
		// The Memory object has a single instance, so each counter has
		// one value.
		instances, err := counter.Values()
		if err != nil {
			continue
		}
		for _, value := range instances {
			values[path] = value
		}
	}
	withRates := r.read
	r.read = true
	return group(counterItems(values, withRates, float64(os.Getpagesize())))
}
