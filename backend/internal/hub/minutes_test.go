package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/history"
)

// minutesDevice is a usage-control whose clock is behind by an hour and that
// kept a minute for every one of the last few, as while the hub could not
// reach it. It hands them out three per answer and remembers what it was
// asked.
type minutesDevice struct {
	clock   time.Duration
	minutes []history.Minute
	asked   []int64
	// broken, when set, answers GET /api/minutes in place of the device.
	broken http.HandlerFunc
}

func newMinutesDevice(now time.Time, kept int) *minutesDevice {
	d := &minutesDevice{clock: -time.Hour}
	deviceNow := now.Add(d.clock).Truncate(time.Minute)
	for i := kept; i >= 1; i-- {
		d.minutes = append(d.minutes, history.Minute{
			Time:   deviceNow.Add(-time.Duration(i) * time.Minute).Unix(),
			Values: map[string]float64{history.MetricCPU: float64(i)},
		})
	}
	return d
}

func (d *minutesDevice) start(t *testing.T) *Agent {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().Add(d.clock)
		switch r.URL.Path {
		case "/api/metrics":
			_, _ = fmt.Fprintf(w, `{"time":%q,"cpu":{"usagePercent":1}}`, now.UTC().Format(time.RFC3339Nano))
		case MinutesPath:
			after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
			d.asked = append(d.asked, after)
			if d.broken != nil {
				d.broken(w, r)
				return
			}
			if after > now.Unix() {
				http.Error(w, "after is later than this device's time", http.StatusBadRequest)
				return
			}
			answer := MinutesAnswer{Now: now.Unix(), Minutes: []history.Minute{}}
			for _, m := range d.minutes {
				if m.Time <= after {
					continue
				}
				if len(answer.Minutes) == 3 {
					answer.More = true
					break
				}
				answer.Minutes = append(answer.Minutes, m)
			}
			_ = json.NewEncoder(w).Encode(answer)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return NewAgent(strings.TrimPrefix(server.URL, "http://"))
}

// storedTimes returns the times of the CPU usage the hub stored for the
// device over the last two hours, a minute apart.
func storedTimes(t *testing.T, store *history.Store, device string) []int64 {
	t.Helper()
	got, err := store.Range(context.Background(), device, time.Now().Add(-2*time.Hour), time.Now().Add(time.Minute), time.Minute)
	if err != nil || len(got) > 1 {
		t.Fatalf("Range() = %+v, %v", got, err)
	}
	var times []int64
	for _, series := range got {
		for _, p := range series.Points {
			times = append(times, p.Time)
		}
	}
	return times
}

// minutesFrom returns the Unix times of the whole minutes from from up to and
// including to.
func minutesFrom(from, to time.Time) []int64 {
	var times []int64
	for at := from; !at.After(to); at = at.Add(time.Minute) {
		times = append(times, at.Unix())
	}
	return times
}

func TestFetcherFillsTheGapFromTheMinutesTheDeviceKept(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Now()
	device := newMinutesDevice(now, 10)
	agent := device.start(t)
	// The hub stored minutes until eight minutes ago, then could not reach the device.
	newest := now.Truncate(time.Minute).Add(-8 * time.Minute)
	if err := store.Add(ctx, "office-pc", newest, map[string]float64{history.MetricCPU: 99}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if _, err := agent.Collect(ctx); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	f := &fetcher{agent: agent, store: store, device: "office-pc", maxValues: maxValues(history.DefaultMaxEntries)}

	if !f.fetch(ctx) {
		t.Fatal("fetch() = false, want true for a device that keeps its minutes")
	}

	got, err := store.Range(ctx, "office-pc", now.Add(-time.Hour), now.Add(time.Minute), time.Minute)
	if err != nil || len(got) != 1 {
		t.Fatalf("Range() = %+v, %v", got, err)
	}
	var times []int64
	for _, p := range got[0].Points {
		times = append(times, p.Time)
	}
	want := []int64{newest.Unix()}
	for i := 7; i >= 1; i-- {
		want = append(want, now.Truncate(time.Minute).Add(-time.Duration(i)*time.Minute).Unix())
	}
	if fmt.Sprint(times) != fmt.Sprint(want) {
		t.Errorf("stored minutes at %v, want %v: the ones after the hub's newest, at the hub's time", times, want)
	}
	// The device was asked in pages, each after the last minute stored.
	if len(device.asked) < 3 || device.asked[0] > newest.Add(device.clock).Unix() {
		t.Errorf("asked after %v, want three pages, the first after the hub's newest minute", device.asked)
	}

	// The next fetch tells the device that the hub has the last minute.
	f.fetch(ctx)
	if last, want := device.asked[len(device.asked)-1], device.minutes[len(device.minutes)-1].Time; last != want {
		t.Errorf("next fetch asked after %d, want %d, the last minute stored", last, want)
	}
}

func TestFetcherStartsWithANewDeviceNow(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	device := newMinutesDevice(time.Now(), 10)
	agent := device.start(t)
	if _, err := agent.Collect(ctx); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	f := &fetcher{agent: agent, store: store, device: "office-pc", maxValues: maxValues(history.DefaultMaxEntries)}

	f.fetch(ctx)

	got, err := store.Range(ctx, "office-pc", time.Now().Add(-time.Hour), time.Now().Add(time.Minute), time.Minute)
	if err != nil || (len(got) > 0 && len(got[0].Points) > 1) {
		t.Errorf("Range() = %+v, %v; want at most the newest minute, not everything the device kept", got, err)
	}
}

func TestFetcherLeavesAnOlderDeviceToTheRecorder(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/metrics" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"cpu":{"usagePercent":1}}`))
	}))
	defer server.Close()
	agent := NewAgent(strings.TrimPrefix(server.URL, "http://"))
	if _, err := agent.Collect(ctx); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	f := &fetcher{agent: agent, store: openTestStore(t), device: "old-pi", maxValues: 10}

	if f.fetch(ctx) {
		t.Error("fetch() = true, want false for a device without minutes")
	}
	if f.fetch(ctx) || f.tooOld.IsZero() {
		t.Error("fetch() again = true, want false without asking until recheckAfter")
	}
}

func TestFetcherWaitsForAnUnreachableDevice(t *testing.T) {
	ctx := context.Background()
	device := newMinutesDevice(time.Now(), 10)
	agent := device.start(t)
	f := &fetcher{agent: agent, store: openTestStore(t), device: "office-pc", maxValues: 10}

	if !f.fetch(ctx) || len(device.asked) != 0 {
		t.Errorf("fetch() before the device answered asked %d times; want true without asking", len(device.asked))
	}
}

func TestFetcherCleansTheMinutesOfTheDevice(t *testing.T) {
	f := &fetcher{after: 1000, offset: 3600, maxValues: 2}
	answer := MinutesAnswer{Now: 2000, Minutes: []history.Minute{
		{Time: 900, Values: map[string]float64{"cpu": 1}},                     // the hub has it
		{Time: 1060, Values: map[string]float64{"cpu": 2, "bad": math.NaN()}}, // NaN dropped
		{Time: 1061, Values: map[string]float64{"cpu": 3}},                    // too close to the one before
		{Time: 1120, Values: map[string]float64{strings.Repeat("x", 300): 4}}, // only a too long name
		{Time: 1180, Values: map[string]float64{"a": 1, "b": 2, "c": 3}},      // too many values
		{Time: 2060, Values: map[string]float64{"cpu": 5}},                    // in the device's future
	}}

	got, after := f.clean(answer)

	// At the hub's time, on the whole minute they fall in.
	first, second := int64(1060+3600)/60*60, int64(1180+3600)/60*60
	if len(got) != 2 || got[0].Time != first || len(got[0].Values) != 1 || got[1].Time != second || len(got[1].Values) != 2 {
		t.Errorf("clean() = %+v, want the minutes at %d with only cpu and at %d with two values", got, first, second)
	}
	if after != 1180 || f.after != 1000 {
		t.Errorf("after = %d, fetcher's after %d; want 1180, the last minute taken, and the fetcher's unchanged until they are stored", after, f.after)
	}
}

func TestFetcherKeepsTheMinutesInPlaceWhileTheClocksDifferBySecondsMoreOrLess(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Now()
	device := newMinutesDevice(now, 10)
	agent := device.start(t)
	newest := now.Truncate(time.Minute).Add(-8 * time.Minute)
	if err := store.Add(ctx, "office-pc", newest, map[string]float64{history.MetricCPU: 99}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	f := &fetcher{agent: agent, store: store, device: "office-pc", maxValues: maxValues(history.DefaultMaxEntries)}
	if _, err := agent.Collect(ctx); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	f.fetch(ctx)

	// The device's clock now seems a second ahead, which would put its next
	// minute on the hub's minute before, which has one already.
	device.clock += time.Second
	device.minutes = append(device.minutes, history.Minute{
		Time:   now.Add(-time.Hour).Truncate(time.Minute).Unix(),
		Values: map[string]float64{history.MetricCPU: 0},
	})
	if _, err := agent.Collect(ctx); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	f.fetch(ctx)

	got, want := storedTimes(t, store, "office-pc"), minutesFrom(newest, now.Truncate(time.Minute))
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("stored minutes at %v, want %v: each minute in its own place", got, want)
	}
}

func TestFetcherStartsAgainWhenTheClockOfTheDeviceGoesBack(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Now()
	device := newMinutesDevice(now, 10)
	agent := device.start(t)
	newest := now.Truncate(time.Minute).Add(-8 * time.Minute)
	if err := store.Add(ctx, "office-pc", newest, map[string]float64{history.MetricCPU: 99}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	f := &fetcher{agent: agent, store: store, device: "office-pc", maxValues: maxValues(history.DefaultMaxEntries)}
	if _, err := agent.Collect(ctx); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	f.fetch(ctx)

	// The device's clock goes back another hour, and it keeps its next minute
	// at its new time, before every minute the hub has from it.
	device.clock -= time.Hour
	device.minutes = []history.Minute{{
		Time:   now.Add(device.clock).Truncate(time.Minute).Unix(),
		Values: map[string]float64{history.MetricCPU: 0},
	}}
	if _, err := agent.Collect(ctx); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if !f.fetch(ctx) {
		t.Fatal("fetch() after the clock went back = false, want true")
	}
	if last := device.asked[len(device.asked)-1]; last > now.Add(device.clock).Unix() {
		t.Errorf("asked after %d, later than the device's time %d", last, now.Add(device.clock).Unix())
	}
	got, want := storedTimes(t, store, "office-pc"), minutesFrom(newest, now.Truncate(time.Minute))
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("stored minutes at %v, want %v: the minute after the clock went back at the hub's time", got, want)
	}
}

func TestFetcherStartsAfterTheHubsOwnMinutesOnceAnOlderDeviceIsUpdated(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Now()
	device := newMinutesDevice(now, 60)
	device.broken = http.NotFound
	agent := device.start(t)
	if err := store.Add(ctx, "old-pi", now.Truncate(time.Minute).Add(-time.Hour), map[string]float64{history.MetricCPU: 99}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if _, err := agent.Collect(ctx); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	f := &fetcher{agent: agent, store: store, device: "old-pi", maxValues: maxValues(history.DefaultMaxEntries)}
	if f.fetch(ctx) || f.after != 0 {
		t.Fatalf("fetch() of an older device = true, after %d; want false and after unset", f.after)
	}

	// Meanwhile the recorder stores the minutes, then the device is updated.
	own := minutesFrom(now.Truncate(time.Minute).Add(-3*time.Minute), now.Truncate(time.Minute).Add(-time.Minute))
	for _, at := range own {
		if err := store.Add(ctx, "old-pi", time.Unix(at, 0), map[string]float64{history.MetricCPU: 50}); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}
	device.broken = nil
	f.tooOld = time.Now().Add(-recheckAfter)

	if !f.fetch(ctx) {
		t.Fatal("fetch() of the updated device = false, want true")
	}
	// The device has the last hour; the hub takes what follows its own minutes.
	got, want := storedTimes(t, store, "old-pi"), append([]int64{now.Truncate(time.Minute).Add(-time.Hour).Unix()}, own...)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("stored minutes at %v, want %v: none of the minutes the recorder stored a second time", got, want)
	}
}

func TestFetcherLeavesAMinuteItCannotFetchToTheRecorder(t *testing.T) {
	ctx := context.Background()
	for name, broken := range map[string]http.HandlerFunc{
		"error": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "disk full", http.StatusInternalServerError)
		},
		"not JSON": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{")) },
	} {
		store := openTestStore(t)
		now := time.Now()
		device := newMinutesDevice(now, 10)
		device.broken = broken
		agent := device.start(t)
		if err := store.Add(ctx, "office-pc", now.Truncate(time.Minute).Add(-8*time.Minute), map[string]float64{history.MetricCPU: 99}); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
		if _, err := agent.Collect(ctx); err != nil {
			t.Fatalf("Collect() error = %v", err)
		}
		f := &fetcher{agent: agent, store: store, device: "office-pc", maxValues: maxValues(history.DefaultMaxEntries)}

		if f.fetch(ctx) || f.after != 0 {
			t.Errorf("%s: fetch() = true, after %d; want false, so the recorder stores the minute, and after unset", name, f.after)
		}

		// The recorder stores the minute; once the device answers again, the
		// hub takes what follows it.
		ownMinute := now.Truncate(time.Minute).Add(-2 * time.Minute)
		if err := store.Add(ctx, "office-pc", ownMinute, map[string]float64{history.MetricCPU: 50}); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
		device.broken = nil
		if !f.fetch(ctx) {
			t.Errorf("%s: fetch() once the device answers = false, want true", name)
		}
		if first := device.asked[1]; first < ownMinute.Add(device.clock).Unix()-1 {
			t.Errorf("%s: asked after %d, want after the minute the recorder stored, %d", name, first, ownMinute.Add(device.clock).Unix())
		}
	}
}

func TestFetcherStopsInTimeForTheReadings(t *testing.T) {
	ctx := context.Background()
	device := newMinutesDevice(time.Now(), 10)
	device.broken = func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }
	agent := device.start(t)
	if _, err := agent.Collect(ctx); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	f := &fetcher{agent: agent, store: openTestStore(t), device: "office-pc", maxValues: 10, budget: 100 * time.Millisecond}

	start := time.Now()
	fetched := f.fetch(ctx)

	if took := time.Since(start); took > time.Second {
		t.Errorf("fetch() took %v, want it to stop after its budget", took)
	}
	// Running out of time is no failure: the rest follows next minute.
	if !fetched || f.failing || f.after == 0 {
		t.Errorf("fetch() = %v, failing %v, after %d; want true, not failing and after kept", fetched, f.failing, f.after)
	}
}
