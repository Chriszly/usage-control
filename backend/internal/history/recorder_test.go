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
	recorder.store(ctx, now.Add(-time.Minute), now, recorder.minuteOf(now))

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
	recorder.store(ctx, now.Add(-time.Minute), now, recorder.minuteOf(now))

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
			Fetch:     func(context.Context, time.Time) bool { calls++; return fetched },
		}

		recorder.read(ctx)
		recorder.store(ctx, now.Add(-time.Minute), now, recorder.minuteOf(now))

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
		fetch func(context.Context, time.Time) bool
		want  bool
	}{
		"in a new minute":                   {last: now.Truncate(time.Minute).Add(-time.Minute), want: true},
		"before storing any":                {want: true},
		"in the minute stored last":         {last: now.Truncate(time.Minute)},
		"for a device the hub fetches from": {fetch: func(context.Context, time.Time) bool { return true }},
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
	for _, c := range []struct {
		now  time.Time
		lag  time.Duration
		want time.Time
	}{
		{now: minute.Add(10 * time.Second), want: minute.Add(storeAt)},
		// Too close to the minute's: the next one's.
		{now: minute.Add(storeAt - time.Second), want: minute.Add(time.Minute + storeAt)},
		// Just after storing, or a timer that fired a little early.
		{now: minute.Add(storeAt + time.Millisecond), want: minute.Add(time.Minute + storeAt)},
		{now: minute.Add(storeAt - time.Millisecond), want: minute.Add(time.Minute + storeAt)},
		// A hub's recorder of another device, at second 5 of the next minute.
		{now: minute.Add(10 * time.Second), lag: fetchLag, want: minute.Add(time.Minute + 5*time.Second)},
		{now: minute.Add(-20 * time.Second), lag: fetchLag, want: minute.Add(5 * time.Second)},
		{now: minute.Add(5*time.Second + time.Millisecond), lag: fetchLag, want: minute.Add(time.Minute + 5*time.Second)},
	} {
		if got := c.now.Add(untilStore(c.now, c.lag)); !got.Equal(c.want) {
			t.Errorf("untilStore(%v, %v) stores at %v, want %v", c.now, c.lag, got, c.want)
		}
	}
}

func TestRecorderStoresUnderTheMinuteItWasDueFor(t *testing.T) {
	minute := time.Unix(1_800_000_000, 0).Truncate(time.Minute)
	for _, lag := range []time.Duration{0, fetchLag} {
		recorder := &Recorder{}
		if lag != 0 {
			recorder.Fetch = func(context.Context, time.Time) bool { return true }
		}
		// Due for the minute before minute, whatever the clock shows when the
		// timer fires.
		wait, due := recorder.next(minute.Add(-40*time.Second), time.Time{})
		if want := minute.Add(-time.Minute); !due.Equal(want) || wait != 40*time.Second+storeAt+lag-time.Minute {
			t.Errorf("lag %v: next() = %v, %v; want %v, at its storeAt", lag, wait, due, want)
		}
		// The clock was set back by half a minute after it stored that
		// minute: the next one is due, not the same one again.
		stored := minute.Add(-time.Minute)
		at := stored.Add(storeAt + lag - 30*time.Second)
		if wait, due := recorder.next(at, stored); !due.Equal(minute) || !at.Add(wait).Equal(minute.Add(storeAt+lag)) {
			t.Errorf("lag %v: next() after the clock went back = %v, %v; want %v at its storeAt", lag, wait, due, minute)
		}
		// The clock was set forward by half a minute: the minute it shows is
		// due next, after the one it stored.
		at = stored.Add(storeAt + lag + 30*time.Second)
		if _, due := recorder.next(at, stored); !due.Equal(minute) {
			t.Errorf("lag %v: next() after the clock went forward = %v; want %v", lag, due, minute)
		}
		// The clock was set back by five minutes after it stored that
		// minute: the minutes before it, kept already, are not stored again.
		at = stored.Add(storeAt + lag - 5*time.Minute)
		wait, due = recorder.next(at, stored)
		if !due.Before(stored) {
			t.Fatalf("lag %v: next() after the clock went back 5 minutes = %v, want a minute before %v", lag, due, stored)
		}
		if got, ok := recorder.storedUnder(at.Add(wait), due, stored); ok {
			t.Errorf("lag %v: storedUnder() after the clock went back 5 minutes = %v, true; want false", lag, got)
		}
	}
}

func TestRecorderStoresTheMinuteAfterAStall(t *testing.T) {
	minute := time.Unix(1_800_000_000, 0).Truncate(time.Minute)
	for _, lag := range []time.Duration{0, fetchLag} {
		recorder := &Recorder{}
		if lag != 0 {
			recorder.Fetch = func(context.Context, time.Time) bool { return true }
		}
		last := minute.Add(-time.Minute)
		at := minute.Add(storeAt + lag)
		// Storing last took 50 seconds: the next minute's time is 10 seconds
		// away, too close for untilStore, and is still waited for.
		if wait, due := recorder.next(at.Add(-10*time.Second), last); !due.Equal(minute) || wait != 10*time.Second {
			t.Errorf("lag %v: next() 10 s before = %v, %v; want %v in 10s", lag, wait, due, minute)
		}
		// 45 seconds late: stored at once, under the minute it was due for.
		now := at.Add(45 * time.Second)
		wait, due := recorder.next(now, last)
		if !due.Equal(minute) || wait != 0 {
			t.Errorf("lag %v: next() 45 s late = %v, %v; want %v at once", lag, wait, due, minute)
		}
		if got, ok := recorder.storedUnder(now, due, last); !ok || !got.Equal(minute) {
			t.Errorf("lag %v: storedUnder() 45 s late = %v, %v; want %v", lag, got, ok, minute)
		}
		// Then the one after it at its own time.
		if wait, due := recorder.next(now, minute); !due.Equal(minute.Add(time.Minute)) || !now.Add(wait).Equal(at.Add(time.Minute)) {
			t.Errorf("lag %v: next() after catching up = %v, %v; want %v at its storeAt", lag, wait, due, minute.Add(time.Minute))
		}
		// More than a minute late, it is no longer due, but the one after it
		// still is: stored at once.
		now = at.Add(90 * time.Second)
		wait, due = recorder.next(now, last)
		if !due.Equal(minute.Add(time.Minute)) || wait != 0 {
			t.Errorf("lag %v: next() 90 s late = %v, %v; want %v at once", lag, wait, due, minute.Add(time.Minute))
		}
		if got, ok := recorder.storedUnder(now, due, last); !ok || !got.Equal(minute.Add(time.Minute)) {
			t.Errorf("lag %v: storedUnder() 90 s late = %v, %v; want %v", lag, got, ok, minute.Add(time.Minute))
		}
		// Five and a half minutes late, the minute whose time came 30
		// seconds ago is the oldest still due.
		now = at.Add(330 * time.Second)
		if wait, due := recorder.next(now, last); !due.Equal(minute.Add(5*time.Minute)) || wait != 0 {
			t.Errorf("lag %v: next() 330 s late = %v, %v; want %v at once", lag, wait, due, minute.Add(5*time.Minute))
		}
		// After catching it up, the minute after it is waited for.
		if wait, due := recorder.next(at.Add(50*time.Second), minute.Add(time.Minute)); !due.Equal(minute.Add(2*time.Minute)) || wait != 70*time.Second {
			t.Errorf("lag %v: next() after the caught up minute = %v, %v; want %v in 70s", lag, wait, due, minute.Add(2*time.Minute))
		}
	}
}

func TestRecorderStoresUnderTheMinuteTheClockShowsAfterAJump(t *testing.T) {
	minute := time.Unix(1_800_000_000, 0).Truncate(time.Minute)
	for _, lag := range []time.Duration{0, fetchLag} {
		recorder := &Recorder{}
		if lag != 0 {
			recorder.Fetch = func(context.Context, time.Time) bool { return true }
		}
		at := minute.Add(storeAt + lag)
		last := minute.Add(-time.Minute)
		// On time, or with the clock set by some seconds: the minute it was due for.
		// A step of 45 seconds, as by NTP after a start from a saved time,
		// keeps it too, so no minute is lost.
		for _, off := range []time.Duration{0, 20 * time.Second, -20 * time.Second, 45 * time.Second, -45 * time.Second} {
			if got, ok := recorder.storedUnder(at.Add(off), minute, last); !ok || !got.Equal(minute) {
				t.Errorf("lag %v, off %v: storedUnder() = %v, %v; want %v", lag, off, got, ok, minute)
			}
		}
		// The clock jumped hours forward, as by NTP after a start from a saved
		// time or a resume from suspend: the minute the clock shows.
		later := at.Add(3 * time.Hour)
		if got, ok := recorder.storedUnder(later, minute, last); !ok || !got.Equal(minute.Add(3*time.Hour)) {
			t.Errorf("lag %v: storedUnder() after a jump forward = %v, %v; want %v", lag, got, ok, minute.Add(3*time.Hour))
		}
		// Hours back: before the minute stored last, so nothing is stored.
		if got, ok := recorder.storedUnder(at.Add(-3*time.Hour), minute, last); ok {
			t.Errorf("lag %v: storedUnder() after a jump back = %v, true; want nothing stored", lag, got)
		}
		// The next minute is due after the one the clock shows.
		if _, due := recorder.next(later, minute.Add(3*time.Hour)); !due.Equal(minute.Add(3*time.Hour + time.Minute)) {
			t.Errorf("lag %v: next() after a jump forward = %v, want %v", lag, due, minute.Add(3*time.Hour+time.Minute))
		}
	}
}

func TestRecorderOfAnotherDeviceStoresUnderTheMinuteBefore(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	// Second 5 of a minute, when a hub's recorder of another device stores.
	now := time.Now().Truncate(time.Minute).Add(5 * time.Second)
	recorder := &Recorder{
		Store:     store,
		Recent:    &Recent{},
		Collector: &sequenceCollector{[]metrics.Snapshot{{Time: now.Add(-30 * time.Second), CPU: metrics.CPU{UsagePercent: 30}}}},
		Device:    "living-room-pi",
		Fetch:     func(context.Context, time.Time) bool { return false },
	}
	recorder.read(ctx)

	recorder.store(ctx, now.Add(-time.Minute), now, recorder.minuteOf(now))

	got, err := store.Range(ctx, "living-room-pi", now.Add(-time.Hour), now.Add(time.Minute), time.Minute)
	if want := now.Truncate(time.Minute).Add(-time.Minute).Unix(); err != nil || len(got) == 0 || got[0].Points[0].Time != want {
		t.Errorf("Range() = %+v, %v; want the average under %d, the minute its readings are in", got, err, want)
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
