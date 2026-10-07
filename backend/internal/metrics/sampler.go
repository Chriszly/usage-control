package metrics

import (
	"context"
	"sync"
	"time"
)

// SamplingInterval is how long a reading of the machine's usage is served
// again. The dashboard refreshes at the same pace.
const SamplingInterval = 2 * time.Second

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

	// reading lets one reading run at a time, so requests that arrive
	// together share it.
	reading sync.Mutex

	// mu guards the newest reading.
	mu       sync.Mutex
	latest   Snapshot
	err      error
	latestAt time.Time
}

// NewSampler returns a Sampler reading from collector.
func NewSampler(collector *Collector) *Sampler {
	return &Sampler{source: collector, interval: SamplingInterval}
}

// Collect returns the newest snapshot, reading one first when the last is an
// interval old.
func (s *Sampler) Collect(ctx context.Context) (Snapshot, error) {
	if s.isFresh() {
		return s.newest()
	}
	s.reading.Lock()
	defer s.reading.Unlock()
	// Another request may have read while this one waited for its turn.
	if s.isFresh() {
		return s.newest()
	}
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
