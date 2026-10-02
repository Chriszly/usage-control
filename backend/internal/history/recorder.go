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

// Collector reads the current usage of the machine.
type Collector interface {
	Collect(ctx context.Context) (metrics.Snapshot, error)
}

// Recorder reads the machine's usage every RecentInterval into Recent, and
// every SampleInterval stores the average of those readings in Store and
// deletes the values older than Retention.
type Recorder struct {
	Store     *Store
	Recent    *Recent
	Collector Collector
	Retention time.Duration
}

// Run records until ctx is cancelled. Failures are logged and the next
// reading is tried again, so a full disk does not stop the website.
func (r *Recorder) Run(ctx context.Context) {
	r.prune(ctx)
	// The first reading measures CPU usage since the machine booted and no
	// network speed, so it only starts the first interval and is not kept.
	if _, err := r.Collector.Collect(ctx); err != nil {
		slog.Error("read usage for the history", "error", err)
	}

	read := time.NewTicker(RecentInterval)
	defer read.Stop()
	store := time.NewTicker(SampleInterval)
	defer store.Stop()
	stored := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-read.C:
			r.read(ctx)
		case now := <-store.C:
			r.store(ctx, stored, now)
			r.prune(ctx)
			stored = now
		}
	}
}

func (r *Recorder) read(ctx context.Context) {
	snapshot, err := r.Collector.Collect(ctx)
	if err != nil {
		slog.Error("read usage for the history", "error", err)
		return
	}
	r.Recent.Add(snapshot.Time, values(snapshot))
}

// store saves the average of the readings from from up to to, at time to.
func (r *Recorder) store(ctx context.Context, from, to time.Time) {
	averages := r.Recent.Average(from, to)
	if averages == nil {
		return
	}
	if err := r.Store.Add(ctx, LocalDevice, to, averages); err != nil {
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
		slog.Debug("deleted old history", "values", deleted)
	}
}
