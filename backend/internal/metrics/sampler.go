package metrics

import (
	"context"
	"sync"
	"time"
)

const (
	// SamplingInterval is how often the Sampler reads the machine's usage
	// while someone is interested. The dashboard refreshes at the same pace.
	SamplingInterval = 2 * time.Second
	// samplerIdleAfter is how long after the last request the Sampler stops
	// reading, so a device nobody looks at costs nothing.
	samplerIdleAfter = 30 * time.Second
)

// source reads one snapshot of the machine's usage: the Collector, or a
// stand-in in tests.
type source interface {
	Collect(ctx context.Context) (Snapshot, error)
}

// Sampler reads the machine's usage every SamplingInterval in the background
// and serves the newest snapshot to everyone who asks: every open page, a hub
// collecting from this device and the recorder. That is one reading per
// interval however many ask, and CPU usage and speeds are always measured over
// the same steady interval. It reads only while someone has asked within
// samplerIdleAfter; the first request after a pause reads at once.
type Sampler struct {
	source    source
	ctx       context.Context
	interval  time.Duration
	idleAfter time.Duration

	// reading lets one reading run at a time, so requests that arrive
	// together after a pause share it.
	reading sync.Mutex

	// mu guards the rest.
	mu       sync.Mutex
	latest   Snapshot
	err      error
	latestAt time.Time
	// askedAt is when someone last asked for a snapshot.
	askedAt time.Time
	// running is set while the background reading goes on.
	running bool
}

// NewSampler returns a Sampler reading from collector until ctx is done.
func NewSampler(ctx context.Context, collector *Collector) *Sampler {
	return &Sampler{source: collector, ctx: ctx, interval: SamplingInterval, idleAfter: samplerIdleAfter}
}

// Collect returns the newest snapshot, reading one first when the last is
// older than an interval, and keeps the background reading going.
func (s *Sampler) Collect(ctx context.Context) (Snapshot, error) {
	now := time.Now()
	s.mu.Lock()
	s.askedAt = now
	if !s.running && s.ctx.Err() == nil {
		s.running = true
		go s.run()
	}
	snapshot, err, fresh := s.latest, s.err, s.isFresh(now)
	s.mu.Unlock()
	if fresh {
		return snapshot, err
	}
	return s.readUnlessFresh(ctx)
}

// isFresh reports whether the newest snapshot is recent enough to serve. The
// caller holds mu.
func (s *Sampler) isFresh(now time.Time) bool {
	return !s.latestAt.IsZero() && now.Sub(s.latestAt) < 2*s.interval
}

// readUnlessFresh reads a snapshot, unless another reading finished while
// this one waited for its turn: requests that arrive together after a pause
// then share the one reading.
func (s *Sampler) readUnlessFresh(ctx context.Context) (Snapshot, error) {
	s.reading.Lock()
	defer s.reading.Unlock()
	s.mu.Lock()
	if s.isFresh(time.Now()) {
		defer s.mu.Unlock()
		return s.latest, s.err
	}
	s.mu.Unlock()
	return s.read(ctx)
}

// read reads a snapshot and keeps it as the newest. The caller holds reading.
func (s *Sampler) read(ctx context.Context) (Snapshot, error) {
	snapshot, err := s.source.Collect(ctx)
	s.mu.Lock()
	s.latest, s.err, s.latestAt = snapshot, err, time.Now()
	s.mu.Unlock()
	return snapshot, err
}

// run reads every interval until nobody has asked for idleAfter or the
// Sampler's context is done.
func (s *Sampler) run() {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			s.stop()
			return
		case <-ticker.C:
		}
		s.mu.Lock()
		idle := time.Since(s.askedAt) >= s.idleAfter
		s.mu.Unlock()
		if idle {
			// Checked again under the lock, so a request that arrives right
			// now starts the reading again instead of finding it running.
			if s.stopIfIdle() {
				return
			}
			continue
		}
		s.reading.Lock()
		_, _ = s.read(s.ctx)
		s.reading.Unlock()
	}
}

// stopIfIdle ends the background reading when nobody has asked for idleAfter.
func (s *Sampler) stopIfIdle() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.askedAt) < s.idleAfter {
		return false
	}
	s.running = false
	return true
}

func (s *Sampler) stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
}
