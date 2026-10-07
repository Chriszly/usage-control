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

func TestRecorderStoresTheMinuteItStopsIn(t *testing.T) {
	stopped, cancel := context.WithCancel(context.Background())
	cancel()
	now := time.Now().Truncate(time.Minute).Add(20 * time.Second)
	for name, c := range map[string]struct {
		last  time.Time
		fetch func(context.Context) bool
		want  bool
	}{
		"in a new minute":                   {last: now.Truncate(time.Minute).Add(-time.Minute), want: true},
		"before storing any":                {want: true},
		"in the minute stored last":         {last: now.Truncate(time.Minute)},
		"for a device the hub fetches from": {fetch: func(context.Context) bool { return true }},
	} {
		store := openTestStore(t)
		recorder := &Recorder{
			Store:     store,
			Recent:    &Recent{},
			Collector: &sequenceCollector{[]metrics.Snapshot{{Time: now.Add(-5 * time.Second), CPU: metrics.CPU{UsagePercent: 30}}}},
			Device:    LocalDevice,
			Fetch:     c.fetch,
		}
		recorder.read(context.Background())

		recorder.storeLast(stopped, now.Add(-30*time.Second), c.last, now)

		got, err := store.Range(context.Background(), LocalDevice, now.Add(-time.Hour), now.Add(time.Minute), time.Minute)
		if err != nil {
			t.Fatalf("%s: Range() error = %v", name, err)
		}
		if stored := len(got) > 0; stored != c.want {
			t.Errorf("%s: stored %+v, want a minute stored: %v", name, got, c.want)
		} else if stored && got[0].Points[0].Time != now.Truncate(time.Minute).Unix() {
			t.Errorf("%s: stored at %d, want at %d, the minute it stopped in", name, got[0].Points[0].Time, now.Truncate(time.Minute).Unix())
		}
	}
}

// startedCollector answers every reading with the same snapshot and tells
// when Run took its first.
type startedCollector struct{ started chan struct{} }

func (c *startedCollector) Collect(context.Context) (metrics.Snapshot, error) {
	select {
	case c.started <- struct{}{}:
	default:
	}
	return metrics.Snapshot{Time: time.Now(), CPU: metrics.CPU{UsagePercent: 30}}, nil
}

func TestRecorderRunStoresTheMinuteItStopsIn(t *testing.T) {
	store := openTestStore(t)
	recent := &Recent{}
	collector := &startedCollector{started: make(chan struct{}, 1)}
	recorder := &Recorder{Store: store, Recent: recent, Collector: collector, Device: LocalDevice}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		recorder.Run(ctx)
	}()
	<-collector.started
	// A reading after the start, as the recorder takes every few seconds; the
	// average covers whole seconds, so it ends a second later.
	read := time.Now()
	recent.Add(read, map[string]float64{MetricCPU: 30})
	time.Sleep(time.Until(read.Truncate(time.Second).Add(time.Second)))

	stop()
	<-done

	got, err := store.Range(context.Background(), LocalDevice, read.Add(-time.Hour), read.Add(time.Hour), time.Minute)
	if err != nil || len(got) != 1 || got[0].Points[0].Value != 30 {
		t.Errorf("Range() after Run stopped = %+v, %v; want the minute it stopped in", got, err)
	}
}

func TestUntilStoreWaitsForTheSameSecondOfAMinute(t *testing.T) {
	minute := time.Unix(1_800_000_000, 0).Truncate(time.Minute)
	for _, c := range []struct{ now, want time.Time }{
		{minute.Add(10 * time.Second), minute.Add(storeAt)},
		// Too close to the minute's: the next one's.
		{minute.Add(storeAt - time.Second), minute.Add(time.Minute + storeAt)},
		// Just after storing, or a timer that fired a little early.
		{minute.Add(storeAt + time.Millisecond), minute.Add(time.Minute + storeAt)},
		{minute.Add(storeAt - time.Millisecond), minute.Add(time.Minute + storeAt)},
	} {
		if got := c.now.Add(untilStore(c.now)); !got.Equal(c.want) {
			t.Errorf("untilStore(%v) stores at %v, want %v", c.now, got, c.want)
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
