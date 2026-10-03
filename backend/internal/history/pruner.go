package history

import (
	"context"
	"log/slog"
	"time"
)

// PruneInterval is how often the values older than the retention are deleted:
// once when the pruner starts and then once a day. The history API never
// returns values older than the retention, so the ones waiting for the next
// cleanup are not shown.
const PruneInterval = 24 * time.Hour

// Pruner deletes the values older than Retention from Store every
// PruneInterval: one pruner for every device's history, not one per recorder.
type Pruner struct {
	Store     *Store
	Retention time.Duration
}

// Run prunes until ctx is cancelled. Failures are logged and the next prune
// tries again.
func (p *Pruner) Run(ctx context.Context) {
	p.prune(ctx)
	prune := time.NewTicker(PruneInterval)
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-prune.C:
			p.prune(ctx)
		}
	}
}

// prune deletes the values older than the retention.
func (p *Pruner) prune(ctx context.Context) {
	deleted, err := p.Store.DeleteBefore(ctx, time.Now().Add(-p.Retention))
	if err != nil {
		slog.Error("delete old history", "error", err)
		return
	}
	if deleted > 0 {
		slog.Info("deleted old history", "values", deleted)
	}
}
