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

func TestRecorderStoresTheAverageOfItsReadings(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Now().Truncate(time.Second)
	recorder := &Recorder{
		Store:  store,
		Recent: &Recent{},
		Collector: &sequenceCollector{[]metrics.Snapshot{
			{Time: now.Add(-10 * time.Second), CPU: metrics.CPU{UsagePercent: 40}},
			{Time: now.Add(-5 * time.Second), CPU: metrics.CPU{UsagePercent: 44}},
		}},
		Device: LocalDevice,
	}

	recorder.read(ctx)
	recorder.read(ctx)
	recorder.store(ctx, now.Add(-time.Minute), now)

	got, err := store.Range(ctx, LocalDevice, now.Add(-time.Hour), now.Add(time.Second), time.Second)
	if err != nil {
		t.Fatalf("Range() error = %v", err)
	}
	want := Series{Metric: MetricCPU, Points: []Point{{Time: now.Unix(), Value: 42}}}
	if len(got) == 0 || !reflect.DeepEqual(got[0], want) {
		t.Errorf("Range() = %+v, want it to start with %+v", got, want)
	}
}

func TestRecorderStoresUnderItsDevice(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Now().Truncate(time.Second)
	recent := &Recent{}
	recorder := &Recorder{
		Store:     store,
		Recent:    recent,
		Collector: &sequenceCollector{[]metrics.Snapshot{{Time: now.Add(-5 * time.Second), CPU: metrics.CPU{UsagePercent: 30}}}},
		Device:    "living-room-pi",
	}

	recorder.read(ctx)
	recorder.store(ctx, now.Add(-time.Minute), now)

	local, err := store.Range(ctx, LocalDevice, now.Add(-time.Hour), now.Add(time.Second), time.Second)
	if err != nil || len(local) != 0 {
		t.Errorf("this device's history = %+v, %v; want it empty", local, err)
	}
	reader := Reader{Store: store, Recent: recent, Device: "living-room-pi"}
	got, _, err := reader.Range(ctx, now.Add(-2*time.Hour), now.Add(time.Second))
	if err != nil || len(got) == 0 || got[0].Points[0].Value != 30 {
		t.Errorf("the device's history = %+v, %v; want the CPU usage it read", got, err)
	}
}
