package history

import (
	"context"
	"log/slog"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// SampleInterval is how often the recorder stores the machine's usage. CPU
// usage and network speed are averages over this interval.
const SampleInterval = time.Minute

// pruneInterval is how often values older than the retention are deleted.
const pruneInterval = time.Hour

// Collector reads the current usage of the machine.
type Collector interface {
	Collect(ctx context.Context) (metrics.Snapshot, error)
}

// Recorder stores the machine's usage every SampleInterval and deletes values
// older than Retention.
type Recorder struct {
	Store     *Store
	Collector Collector
	Retention time.Duration
}

// Run records until ctx is cancelled. Failures are logged and the next
// sample is tried again, so a full disk does not stop the website.
func (r *Recorder) Run(ctx context.Context) {
	r.prune(ctx)
	// The first reading measures CPU usage since the machine booted and no
	// network speed, so it only starts the first interval and is not stored.
	if _, err := r.Collector.Collect(ctx); err != nil {
		slog.Error("read usage for the history", "error", err)
	}

	sample := time.NewTicker(SampleInterval)
	defer sample.Stop()
	prune := time.NewTicker(pruneInterval)
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-sample.C:
			r.record(ctx)
		case <-prune.C:
			r.prune(ctx)
		}
	}
}

func (r *Recorder) record(ctx context.Context) {
	snapshot, err := r.Collector.Collect(ctx)
	if err != nil {
		slog.Error("read usage for the history", "error", err)
		return
	}
	if err := r.Store.Add(ctx, LocalDevice, snapshot.Time, values(snapshot)); err != nil {
		slog.Error("store usage in the history", "error", err)
	}
}

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
