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

func newTestSampler(ctx context.Context, src *countingSource) *Sampler {
	return &Sampler{source: src, ctx: ctx, interval: 20 * time.Millisecond, idleAfter: 100 * time.Millisecond}
}

func (s *Sampler) isRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// eventually waits up to a second for condition to hold.
func eventually(t *testing.T, condition func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("waited a second for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSamplerSharesOneReadingAndStopsWhenNobodyAsks(t *testing.T) {
	ctx := context.Background()
	src := &countingSource{}
	sampler := newTestSampler(ctx, src)

	// The first request after a pause reads at once; the next one, right
	// after it, gets the same snapshot.
	first, err := sampler.Collect(ctx)
	second, _ := sampler.Collect(ctx)
	if err != nil || first.UptimeSeconds != 1 || second.UptimeSeconds != 1 || src.readings.Load() != 1 {
		t.Fatalf("two requests = %d, %d after %d readings, %v; want both served from one reading", first.UptimeSeconds, second.UptimeSeconds, src.readings.Load(), err)
	}

	// Meanwhile the background reading goes on every interval.
	eventually(t, func() bool { return src.readings.Load() >= 3 }, "background readings")
	later, _ := sampler.Collect(ctx)
	if later.UptimeSeconds < 2 {
		t.Errorf("a later request = reading %d, want a newer one than the first", later.UptimeSeconds)
	}

	// Once nobody asks for a while, it stops; the next request starts it again.
	eventually(t, func() bool { return !sampler.isRunning() }, "the sampler to stop")
	readings := src.readings.Load()
	time.Sleep(3 * sampler.interval)
	if src.readings.Load() != readings {
		t.Errorf("readings went on after the sampler stopped: %d, then %d", readings, src.readings.Load())
	}
	again, _ := sampler.Collect(ctx)
	if again.UptimeSeconds != uint64(readings)+1 || !sampler.isRunning() { //nolint:gosec // a small test count
		t.Errorf("request after the stop = reading %d, running %v; want a fresh reading %d and the sampler running", again.UptimeSeconds, sampler.isRunning(), readings+1)
	}
}

func TestSamplerReportsAFailedReadingAndEndsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	src := &countingSource{err: errors.New("no /proc")}
	sampler := newTestSampler(ctx, src)

	if _, err := sampler.Collect(context.Background()); err == nil {
		t.Error("Collect() error = nil, want the reading's error")
	}
	cancel()
	eventually(t, func() bool { return !sampler.isRunning() }, "the sampler to end with its context")
	if _, err := sampler.Collect(context.Background()); err == nil || sampler.isRunning() {
		t.Errorf("after the context ended: error = %v, running = %v; want the error and no background reading", err, sampler.isRunning())
	}
}
