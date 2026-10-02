package history

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

type sequenceCollector struct{ snapshots []metrics.Snapshot }

func (f *sequenceCollector) Collect(context.Context) (metrics.Snapshot, error) {
	snapshot := f.snapshots[0]
	f.snapshots = f.snapshots[1:]
	return snapshot, nil
}

func TestRecorderStoresTheAverageAndDeletesOldValues(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Now().Truncate(time.Second)
	if err := store.Add(ctx, LocalDevice, now.AddDate(0, 0, -31), map[string]float64{MetricCPU: 1}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	recorder := &Recorder{
		Store:  store,
		Recent: &Recent{},
		Collector: &sequenceCollector{[]metrics.Snapshot{
			{Time: now.Add(-10 * time.Second), CPU: metrics.CPU{UsagePercent: 40}},
			{Time: now.Add(-5 * time.Second), CPU: metrics.CPU{UsagePercent: 44}},
		}},
		Retention: 30 * 24 * time.Hour,
	}

	recorder.read(ctx)
	recorder.read(ctx)
	recorder.store(ctx, now.Add(-time.Minute), now)
	recorder.prune(ctx)

	got, err := store.Range(ctx, LocalDevice, now.AddDate(0, 0, -40), now.Add(time.Second), time.Second)
	if err != nil {
		t.Fatalf("Range() error = %v", err)
	}
	// The value from 31 days ago is gone; the average of the readings is stored.
	want := Series{Metric: MetricCPU, Points: []Point{{Time: now.Unix(), Value: 42}}}
	if len(got) == 0 || !reflect.DeepEqual(got[0], want) {
		t.Errorf("Range() = %+v, want it to start with %+v", got, want)
	}
}
