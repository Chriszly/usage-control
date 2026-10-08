package metrics

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// SamplingInterval is how long a reading of the machine's usage is served
// again. The dashboard refreshes at the same pace.
const SamplingInterval = 2 * time.Second

// readTimeout is how long one reading of the machine may take.
const readTimeout = 10 * time.Second

// source reads one snapshot of the machine's usage: the Collector, or a
// stand-in in tests.
type source interface {
	Collect(ctx context.Context) (Snapshot, error)
}

// Sampler serves one reading of the machine's usage to everyone who asks
// within SamplingInterval: every open page, a hub collecting from this device
// and the recorder. However many ask, the machine is read at most once per
// interval, and only when someone asks: every few seconds for the recorder or
// a hub, every 2 seconds while a page is open.
type Sampler struct {
	source   source
	interval time.Duration

	// reading holds a value while a reading runs, so requests that arrive
	// together share it. A request that ends while it waits stops waiting.
	reading chan struct{}

	// mu guards the newest reading.
	mu       sync.Mutex
	latest   Snapshot
	err      error
	latestAt time.Time
}

// NewSampler returns a Sampler reading from collector.
func NewSampler(collector *Collector) *Sampler {
	return newSampler(collector, SamplingInterval)
}

func newSampler(src source, interval time.Duration) *Sampler {
	return &Sampler{source: src, interval: interval, reading: make(chan struct{}, 1)}
}

// Collect returns the newest snapshot, reading one first when the last is an
// interval old.
func (s *Sampler) Collect(ctx context.Context) (Snapshot, error) {
	if s.isFresh() {
		return s.newest()
	}
	// A request that has ended still reads when nothing else is reading, as
	// the reading is shared; it only stops waiting for another's.
	select {
	case s.reading <- struct{}{}:
	default:
		select {
		case s.reading <- struct{}{}:
		case <-ctx.Done():
			return Snapshot{}, fmt.Errorf("wait for the reading: %w", ctx.Err())
		}
	}
	defer func() { <-s.reading }()
	// Another request may have read while this one waited for its turn.
	if s.isFresh() {
		return s.newest()
	}
	// The reading is shared, so it does not end with the request that
	// started it, such as a page that was closed; it has its own time limit.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), readTimeout)
	defer cancel()
	snapshot, err := s.source.Collect(ctx)
	s.mu.Lock()
	s.latest, s.err, s.latestAt = snapshot, err, time.Now()
	s.mu.Unlock()
	return snapshot, err
}

// isFresh reports whether the newest snapshot is young enough to serve.
func (s *Sampler) isFresh() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.latestAt.IsZero() && time.Since(s.latestAt) < s.interval
}

func (s *Sampler) newest() (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latest, s.err
}

// Reusing returns a collector for a reader that reads regularly, such as the
// recorder: it serves the newest reading while that is younger than maxAge,
// so when a hub or a page asked meanwhile the machine is not read again.
func (s *Sampler) Reusing(maxAge time.Duration) ReusingSampler {
	return ReusingSampler{sampler: s, maxAge: maxAge}
}

// ReusingSampler is a Sampler that serves readings up to maxAge old; see
// Sampler.Reusing.
type ReusingSampler struct {
	sampler *Sampler
	maxAge  time.Duration
}

// Collect returns the newest snapshot while it is younger than maxAge, and
// otherwise reads one.
func (r ReusingSampler) Collect(ctx context.Context) (Snapshot, error) {
	s := r.sampler
	s.mu.Lock()
	reuse := !s.latestAt.IsZero() && s.err == nil && time.Since(s.latestAt) < r.maxAge
	latest := s.latest
	s.mu.Unlock()
	if reuse {
		return latest, nil
	}
	return s.Collect(ctx)
}
