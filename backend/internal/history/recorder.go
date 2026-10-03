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
// every SampleInterval stores the average of those readings in Store. A
// Pruner deletes what is older than the retention.
type Recorder struct {
	Store     *Store
	Recent    *Recent
	Collector Collector
	// Device is the name the readings are stored under, such as LocalDevice.
	Device string
	// MaxEntries is how many disks, sensors, network cards and GPUs each are
	// kept.
	MaxEntries int

	// failing is set while readings fail, so an unreachable device is logged
	// once and not every few seconds.
	failing bool
	// dropped is set once a reading had more entries than are kept, so that
	// is logged once too.
	dropped bool
}

// Run records until ctx is cancelled. Failures are logged and the next
// reading is tried again, so a full disk does not stop the website.
func (r *Recorder) Run(ctx context.Context) {
	// The first reading measures CPU usage since the machine booted and no
	// network speed, so it only starts the first interval and is not kept.
	if _, err := r.Collector.Collect(ctx); err != nil {
		r.failed(err)
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
			stored = now
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
		slog.Info("reading usage for the history works again", "device", r.Device)
	}
	v, dropped := values(snapshot, r.MaxEntries)
	if dropped && !r.dropped {
		r.dropped = true
		slog.Warn("the device reports more disks, sensors, network cards or GPUs than the history keeps; raise HISTORY_MAX_ENTRIES to keep them all",
			"device", r.Device, "kept", r.MaxEntries)
	}
	r.Recent.Add(snapshot.Time, v)
}

// failed logs a failed reading, unless the previous one failed too.
func (r *Recorder) failed(err error) {
	if !r.failing {
		r.failing = true
		slog.Error("read usage for the history", "device", r.Device, "error", err)
	}
}

// store saves the average of the readings from from up to to, at time to.
func (r *Recorder) store(ctx context.Context, from, to time.Time) {
	averages := r.Recent.Average(from, to)
	if averages == nil {
		return
	}
	if err := r.Store.Add(ctx, r.Device, to, averages); err != nil {
		slog.Error("store usage in the history", "error", err)
	}
}
