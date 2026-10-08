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
	sampler := newSampler(src, 50*time.Millisecond)

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
	sampler := newSampler(&countingSource{err: errors.New("no /proc")}, time.Minute)
	for range 2 {
		if _, err := sampler.Collect(context.Background()); err == nil {
			t.Error("Collect() error = nil, want the reading's error")
		}
	}
}

// contextSource reports the error of the context it reads with.
type contextSource struct{}

func (contextSource) Collect(ctx context.Context) (Snapshot, error) {
	if _, ok := ctx.Deadline(); !ok {
		return Snapshot{}, errors.New("no time limit")
	}
	return Snapshot{}, ctx.Err()
}

func TestSamplerReadingOutlivesTheRequest(t *testing.T) {
	sampler := newSampler(contextSource{}, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sampler.Collect(ctx); err != nil {
		t.Errorf("Collect() for a closed request error = %v, want a reading with its own time limit", err)
	}
}

func TestReusingSamplerTakesAYoungEnoughReading(t *testing.T) {
	src := &countingSource{}
	sampler := newSampler(src, time.Millisecond)
	ctx := context.Background()

	if _, err := sampler.Collect(ctx); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := sampler.Reusing(time.Minute).Collect(ctx); err != nil || src.readings.Load() != 1 {
		t.Errorf("Reusing(1 min).Collect() read %d times, %v; want the reading of a moment ago taken", src.readings.Load(), err)
	}
	if _, err := sampler.Reusing(time.Millisecond).Collect(ctx); err != nil || src.readings.Load() != 2 {
		t.Errorf("Reusing(1 ms).Collect() read %d times, %v; want a new reading", src.readings.Load(), err)
	}
}

// blockingSource reads until release is closed.
type blockingSource struct {
	started chan struct{}
	release chan struct{}
}

func (b blockingSource) Collect(context.Context) (Snapshot, error) {
	close(b.started)
	<-b.release
	return Snapshot{}, nil
}

func TestSamplerStopsWaitingWhenTheRequestEnds(t *testing.T) {
	src := blockingSource{started: make(chan struct{}), release: make(chan struct{})}
	defer close(src.release)
	sampler := newSampler(src, time.Minute)
	go func() { _, _ = sampler.Collect(context.Background()) }()
	<-src.started

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := sampler.Collect(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Collect() while a reading hangs error = %v, want the request's deadline", err)
	}
}
