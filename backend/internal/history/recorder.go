package history

import (
	"context"
	"log/slog"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// SampleInterval is how often the recorder stores the average of its
// readings in the database.
const SampleInterval = time.Minute

// PruneInterval is how often the values older than the retention are deleted:
// once when the recorder starts and then once a day. The history API never
// returns values older than the retention, so the ones waiting for the next
// cleanup are not shown.
const PruneInterval = 24 * time.Hour

// Collector reads the current usage of the machine.
type Collector interface {
	Collect(ctx context.Context) (metrics.Snapshot, error)
}

// Recorder reads the machine's usage every RecentInterval into Recent, and
// every SampleInterval stores the average of those readings in Store. Values
// older than Retention are deleted every PruneInterval.
type Recorder struct {
	Store     *Store
	Recent    *Recent
	Collector Collector
	Retention time.Duration
	// Device is the name the readings are stored under; LocalDevice when empty.
	Device string

	// failing is set while readings fail, so an unreachable device is logged
	// once and not every few seconds.
	failing bool
}

// Run records until ctx is cancelled. Failures are logged and the next
// reading is tried again, so a full disk does not stop the website.
func (r *Recorder) Run(ctx context.Context) {
	r.prune(ctx)
	// The first reading measures CPU usage since the machine booted and no
	// network speed, so it only starts the first interval and is not kept.
	if _, err := r.Collector.Collect(ctx); err != nil {
		r.failed(err)
	}

	read := time.NewTicker(RecentInterval)
	defer read.Stop()
	store := time.NewTicker(SampleInterval)
	defer store.Stop()
	prune := time.NewTicker(PruneInterval)
	defer prune.Stop()
	stored := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-read.C:
			r.read(ctx)
		case now := <-store.C:
			r.store(ctx, stored, now)
			stored = now
		case <-prune.C:
			r.prune(ctx)
		}
	}
}

func (r *Recorder) read(ctx context.Context) {
	snapshot, err := r.Collector.Collect(ctx)
	if err != nil {
		r.failed(err)
		return
	}
	if r.failing {
		r.failing = false
		slog.Info("reading usage for the history works again", "device", deviceOrLocal(r.Device))
	}
	r.Recent.Add(snapshot.Time, values(snapshot))
}

// failed logs a failed reading, unless the previous one failed too.
func (r *Recorder) failed(err error) {
	if !r.failing {
		r.failing = true
		slog.Error("read usage for the history", "device", deviceOrLocal(r.Device), "error", err)
	}
}

// store saves the average of the readings from from up to to, at time to.
func (r *Recorder) store(ctx context.Context, from, to time.Time) {
	averages := r.Recent.Average(from, to)
	if averages == nil {
		return
	}
	if err := r.Store.Add(ctx, deviceOrLocal(r.Device), to, averages); err != nil {
		slog.Error("store usage in the history", "error", err)
	}
}

// prune deletes the values older than the retention.
func (r *Recorder) prune(ctx context.Context) {
	deleted, err := r.Store.DeleteBefore(ctx, time.Now().Add(-r.Retention))
	if err != nil {
		slog.Error("delete old history", "error", err)
		return
	}
	if deleted > 0 {
		slog.Info("deleted old history", "values", deleted)
	}
}
