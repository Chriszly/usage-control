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
	// Stored at the start of the minute, as a hub stores the minutes it fetches.
	want := Series{Metric: MetricCPU, Points: []Point{{Time: now.Truncate(time.Minute).Unix(), Value: 42}}}
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

func TestRecorderLeavesStoringToFetchWhenItCan(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	for _, fetched := range []bool{true, false} {
		store := openTestStore(t)
		calls := 0
		recorder := &Recorder{
			Store:     store,
			Recent:    &Recent{},
			Collector: &sequenceCollector{[]metrics.Snapshot{{Time: now.Add(-5 * time.Second), CPU: metrics.CPU{UsagePercent: 30}}}},
			Device:    "living-room-pi",
			Fetch:     func(context.Context) bool { calls++; return fetched },
		}

		recorder.read(ctx)
		recorder.store(ctx, now.Add(-time.Minute), now)

		got, err := store.Range(ctx, "living-room-pi", now.Add(-time.Hour), now.Add(time.Second), time.Second)
		if err != nil || calls != 1 || (len(got) == 0) != fetched {
			t.Errorf("with Fetch returning %v: %d calls, stored %+v, %v; want the average stored only when Fetch cannot", fetched, calls, got, err)
		}
	}
}

func TestRecorderTakesAReadingOnlyOnce(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	reading := metrics.Snapshot{Time: now.Add(-5 * time.Second), CPU: metrics.CPU{UsagePercent: 30}}
	later := metrics.Snapshot{Time: now, CPU: metrics.CPU{UsagePercent: 60}}
	recent := &Recent{}
	recorder := &Recorder{
		Store:     openTestStore(t),
		Recent:    recent,
		Collector: &sequenceCollector{[]metrics.Snapshot{reading, reading, later}},
		Device:    LocalDevice,
	}

	for range 3 {
		recorder.read(ctx)
	}

	if got := recent.Average(now.Add(-time.Minute), now.Add(time.Second)); got[MetricCPU] != 45 {
		t.Errorf("average CPU = %v, want 45 from two readings, the shared one counted once", got[MetricCPU])
	}
}
