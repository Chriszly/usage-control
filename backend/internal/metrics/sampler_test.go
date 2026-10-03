package metrics

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// countingSource counts its readings and numbers the snapshots by them.
type countingSource struct {
	readings atomic.Int64
	err      error
}

func (c *countingSource) Collect(context.Context) (Snapshot, error) {
	n := c.readings.Add(1)
	return Snapshot{UptimeSeconds: uint64(n)}, c.err //nolint:gosec // a small test count
}

func TestSamplerSharesOneReadingPerInterval(t *testing.T) {
	ctx := context.Background()
	src := &countingSource{}
	sampler := &Sampler{source: src, interval: 50 * time.Millisecond}

	// The first request reads; the next one, right after it, gets the same snapshot.
	first, err := sampler.Collect(ctx)
	second, _ := sampler.Collect(ctx)
	if err != nil || first.UptimeSeconds != 1 || second.UptimeSeconds != 1 || src.readings.Load() != 1 {
		t.Fatalf("two requests = %d, %d after %d readings, %v; want both served from one reading", first.UptimeSeconds, second.UptimeSeconds, src.readings.Load(), err)
	}

	// Nobody asks, nothing is read.
	time.Sleep(3 * sampler.interval)
	if got := src.readings.Load(); got != 1 {
		t.Errorf("readings without requests = %d, want 1", got)
	}

	// Once the reading is an interval old, the next request reads again.
	later, _ := sampler.Collect(ctx)
	if later.UptimeSeconds != 2 {
		t.Errorf("a later request = reading %d, want a fresh reading 2", later.UptimeSeconds)
	}
}

func TestSamplerReportsAFailedReading(t *testing.T) {
	sampler := &Sampler{source: &countingSource{err: errors.New("no /proc")}, interval: time.Minute}
	for range 2 {
		if _, err := sampler.Collect(context.Background()); err == nil {
			t.Error("Collect() error = nil, want the reading's error")
		}
	}
}
