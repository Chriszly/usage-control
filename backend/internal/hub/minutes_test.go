package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/history"
	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// minutesDevice is a usage-control whose clock is behind by an hour and that
// kept a minute for every one of the last few, as while the hub could not
// reach it. It hands them out three per answer and remembers what it was
// asked.
type minutesDevice struct {
	clock   time.Duration
	minutes []history.Minute
	asked   []int64
	// values is how many values each answer was asked to have at most.
	values []int
	// slowAfter, when set, is how many answers the device gives before it
	// answers no more in time.
	slowAfter int
	// extras describes the extras among the minutes.
	extras map[string]history.ExtraInfo
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
			values, _ := strconv.Atoi(r.URL.Query().Get("values"))
			d.values = append(d.values, values)
			if d.slowAfter > 0 && len(d.asked) > d.slowAfter {
				<-r.Context().Done()
				return
			}
			if d.broken != nil {
				d.broken(w, r)
				return
			}
			if after > now.Unix() {
				http.Error(w, "after is later than this device's time", http.StatusBadRequest)
				return
			}
			answer := MinutesAnswer{Now: now.Unix(), Minutes: []history.Minute{}, Extras: d.extras}
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
	// The device keeps the hub's newest minute and the ones after it.
	device := newMinutesDevice(now, 8)
	agent := device.start(t)
	// The hub stored minutes until eight minutes ago, then could not reach the device.
	newest := now.Truncate(time.Minute).Add(-8 * time.Minute)
	if err := store.Add(ctx, "office-pc", newest, map[string]float64{history.MetricCPU: 99}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if _, err := agent.Collect(ctx); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	f := &fetcher{agent: agent, store: store, device: "office-pc", maxEntries: history.DefaultMaxEntries}

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
	f := &fetcher{agent: agent, store: store, device: "office-pc", maxEntries: history.DefaultMaxEntries}

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
	f := &fetcher{agent: agent, store: openTestStore(t), device: "old-pi", maxEntries: 10}

	if f.fetch(ctx) {
		t.Error("fetch() = true, want false for a device without minutes")
	}
	if f.fetch(ctx) || f.tooOld.IsZero() {
		t.Error("fetch() again = true, want false without asking until recheckAfter")
	}
}

func TestFetcherLeavesTheMinuteToTheRecorderWhileTheDeviceDoesNotAnswer(t *testing.T) {
	ctx := context.Background()
	device := newMinutesDevice(time.Now(), 10)
	agent := device.start(t)
	f := &fetcher{agent: agent, store: openTestStore(t), device: "office-pc", maxEntries: 10, back: time.Now().Add(-time.Hour)}

	if f.fetch(ctx) || len(device.asked) != 0 || f.failing {
		t.Errorf("fetch() before the device answered asked %d times; want false without asking or logging, so the recorder stores the readings it has of the minute", len(device.asked))
	}
	if !f.back.IsZero() {
		t.Errorf("back = %v, want zero, so the device gets the time to keep its first minute once it answers again", f.back)
	}
}

func TestFetcherCleansTheMinutesOfTheDevice(t *testing.T) {
	f := &fetcher{after: 1000, offset: 3600, maxEntries: 2}
	answer := MinutesAnswer{Now: 2000, Minutes: []history.Minute{
		{Time: 900, Values: map[string]float64{"cpu": 1}},                                  // the hub has it
		{Time: 1060, Values: map[string]float64{"cpu": 2, "memory": math.NaN()}},           // NaN dropped
		{Time: 1061, Values: map[string]float64{"cpu": 3}},                                 // too close to the one before
		{Time: 1120, Values: map[string]float64{"disk:/" + strings.Repeat("x", 300): 4}},   // only a too long name
		{Time: 1180, Values: map[string]float64{"disk:/a": 1, "disk:/b": 2, "disk:/c": 3}}, // too many disks
		{Time: 2060, Values: map[string]float64{"cpu": 5}},                                 // in the device's future
	}}

	got, after := f.clean(answer)

	// At the hub's time, on the whole minute they fall in.
	first, second := int64(1060+3600)/60*60, int64(1180+3600)/60*60
	if len(got) != 2 || got[0].Time != first || len(got[0].Values) != 1 || got[1].Time != second || len(got[1].Values) != 2 {
		t.Errorf("clean() = %+v, want the minutes at %d with only cpu and at %d with two values", got, first, second)
	}
	if !f.dropped {
		t.Error("dropped = false, want it set once disks were left out, so that is logged")
	}
	if after != 1180 || f.after != 1000 {
		t.Errorf("after = %d, fetcher's after %d; want 1180, the last minute taken, and the fetcher's unchanged until they are stored", after, f.after)
	}
}

func TestFetcherKeepsTheMinutesInPlaceWhileTheClocksDifferBySecondsMoreOrLess(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Now()
	// The device keeps the hub's newest minute and the ones after it.
	device := newMinutesDevice(now, 8)
	agent := device.start(t)
	newest := now.Truncate(time.Minute).Add(-8 * time.Minute)
	if err := store.Add(ctx, "office-pc", newest, map[string]float64{history.MetricCPU: 99}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	f := &fetcher{agent: agent, store: store, device: "office-pc", maxEntries: history.DefaultMaxEntries}
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
	// The device keeps the hub's newest minute and the ones after it.
	device := newMinutesDevice(now, 8)
	agent := device.start(t)
	newest := now.Truncate(time.Minute).Add(-8 * time.Minute)
	if err := store.Add(ctx, "office-pc", newest, map[string]float64{history.MetricCPU: 99}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	f := &fetcher{agent: agent, store: store, device: "office-pc", maxEntries: history.DefaultMaxEntries}
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
	f := &fetcher{agent: agent, store: store, device: "old-pi", maxEntries: history.DefaultMaxEntries}
	if f.fetch(ctx) || f.after != 0 {
		t.Fatalf("fetch() of an older device = true, after %d; want false and after unset", f.after)
	}

	// Meanwhile the recorder stores the minutes, then the device is updated.
	own := minutesFrom(now.Truncate(time.Minute).Add(-8*time.Minute), now.Truncate(time.Minute).Add(-2*time.Minute))
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
	want = append(want, now.Truncate(time.Minute).Add(-time.Minute).Unix())
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
		device := newMinutesDevice(now, 8)
		device.broken = broken
		agent := device.start(t)
		if err := store.Add(ctx, "office-pc", now.Truncate(time.Minute).Add(-8*time.Minute), map[string]float64{history.MetricCPU: 99}); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
		if _, err := agent.Collect(ctx); err != nil {
			t.Fatalf("Collect() error = %v", err)
		}
		f := &fetcher{agent: agent, store: store, device: "office-pc", maxEntries: history.DefaultMaxEntries}

		if f.fetch(ctx) {
			t.Errorf("%s: fetch() = true, want false, so the recorder stores the minute", name)
		}

		// The recorder stores the minute; once the device answers again, the
		// hub still takes every minute the device kept, but keeps its own.
		ownMinute := now.Truncate(time.Minute).Add(-2 * time.Minute)
		if err := store.Add(ctx, "office-pc", ownMinute, map[string]float64{history.MetricCPU: 50}); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
		device.broken = nil
		if !f.fetch(ctx) {
			t.Errorf("%s: fetch() once the device answers = false, want true", name)
		}
		if device.asked[1] != device.asked[0] {
			t.Errorf("%s: asked after %d once the device answers, want after %d as before the failure", name, device.asked[1], device.asked[0])
		}
		if got, want := storedTimes(t, store, "office-pc"), minutesFrom(now.Truncate(time.Minute).Add(-8*time.Minute), now.Truncate(time.Minute).Add(-time.Minute)); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s: stored minutes %v, want every one from the hub's newest on, %v", name, got, want)
		}
		got, err := store.Range(ctx, "office-pc", ownMinute, ownMinute.Add(time.Minute), time.Minute)
		if err != nil || len(got) != 1 || got[0].Points[0].Value != 50 {
			t.Errorf("%s: Range() = %+v, %v; want the recorder's own minute kept", name, got, err)
		}
	}
}

func TestFetcherFetchesTheDevicesMinuteBeforeOneTheHubStoredItselfAfterARestart(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Now()
	// The hub fetched the device's minute three minutes ago. The device kept
	// the next one and was switched off, so the hub stored the one after that
	// itself, and then the hub was restarted.
	device := newMinutesDevice(now, 3)
	device.minutes = device.minutes[:2]
	agent := device.start(t)
	fetched, own := now.Truncate(time.Minute).Add(-3*time.Minute), now.Truncate(time.Minute).Add(-time.Minute)
	for at, cpu := range map[time.Time]float64{fetched: 3, own: 50} {
		if err := store.Add(ctx, "office-pc", at, map[string]float64{history.MetricCPU: cpu}); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}
	if _, err := agent.Collect(ctx); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	f := &fetcher{agent: agent, store: store, device: "office-pc", maxEntries: history.DefaultMaxEntries}

	if !f.fetch(ctx) {
		t.Fatal("fetch() = false, want true for a device that keeps its minutes")
	}
	if got, want := storedTimes(t, store, "office-pc"), minutesFrom(fetched, own); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("stored minutes at %v, want %v: the device's minute before the one the hub stored itself too", got, want)
	}
}

func TestFetcherStartsAgainWhenTheDeviceRefusesAfter(t *testing.T) {
	ctx := context.Background()
	device := newMinutesDevice(time.Now(), 10)
	agent := device.start(t)
	if _, err := agent.Collect(ctx); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	f := &fetcher{agent: agent, store: openTestStore(t), device: "office-pc", maxEntries: history.DefaultMaxEntries}
	device.broken = func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "after is later than this device's time", http.StatusBadRequest)
	}

	if f.fetch(ctx) || f.after != 0 {
		t.Errorf("fetch() after a refusal: after = %d, want 0, so the next fetch works it out again", f.after)
	}
}

func TestFetcherLeavesAMinuteToTheRecorderWhenTheDeviceKeepsNone(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	device := newMinutesDevice(now, 10)
	// The device answers, but has kept nothing for the last five minutes,
	// as when it cannot write to its disk.
	device.minutes = device.minutes[:5]
	agent := device.start(t)
	if _, err := agent.Collect(ctx); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	store := openTestStore(t)
	if err := store.Add(ctx, "office-pc", now.Truncate(time.Minute).Add(-11*time.Minute), map[string]float64{history.MetricCPU: 99}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	f := &fetcher{agent: agent, store: store, device: "office-pc", maxEntries: history.DefaultMaxEntries, back: now.Add(-time.Hour)}

	if f.fetch(ctx) {
		t.Error("fetch() = true for a device that keeps no minutes now, want false, so the recorder stores its own")
	}
	if got := storedTimes(t, store, "office-pc"); len(got) != 6 {
		t.Errorf("stored minutes %v, want the hub's own and the five the device kept", got)
	}
}

func TestFetcherLeavesTheMinutesToTheRecorderQuietlyUntilADeviceThatIsBackKeepsOne(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	// The device was switched off for an hour and has just started again,
	// so it has kept no minute since.
	device := newMinutesDevice(now.Add(-time.Hour), 10)
	agent := device.start(t)
	if _, err := agent.Collect(ctx); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	store := openTestStore(t)
	if err := store.Add(ctx, "office-pc", now.Truncate(time.Minute).Add(-time.Hour), map[string]float64{history.MetricCPU: 99}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	f := &fetcher{agent: agent, store: store, device: "office-pc", maxEntries: history.DefaultMaxEntries}

	if f.fetch(ctx) || f.failing {
		t.Error("fetch() for a device that is back = true or logged a failure; want false without logging, so the recorder stores its own until the device keeps its first minute")
	}
}

func TestFetcherLeavesAMinuteInTheHubsFutureForTheNextFetch(t *testing.T) {
	next := time.Now().Truncate(time.Minute).Add(time.Minute).Unix()
	f := &fetcher{after: next - 120, maxEntries: 10}
	answer := MinutesAnswer{Now: next + 1, Minutes: []history.Minute{
		{Time: next - 60, Values: map[string]float64{"cpu": 1}},
		{Time: next, Values: map[string]float64{"cpu": 2}}, // in the hub's future
	}}

	got, after := f.clean(answer)

	if len(got) != 1 || got[0].Time != next-60 || after != next-60 {
		t.Errorf("clean() = %+v, after %d; want only the minute at %d, and after it, so the next fetch asks for the one at %d again", got, after, next-60, next)
	}
}

func TestFetcherStopsInTimeForTheReadings(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Now()
	device := newMinutesDevice(now, 8)
	// The first answer comes in time, the next no more.
	device.slowAfter = 1
	agent := device.start(t)
	if err := store.Add(ctx, "office-pc", now.Truncate(time.Minute).Add(-8*time.Minute), map[string]float64{history.MetricCPU: 99}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if _, err := agent.Collect(ctx); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	f := &fetcher{agent: agent, store: store, device: "office-pc", maxEntries: 10, budget: 200 * time.Millisecond}

	start := time.Now()
	fetched := f.fetch(ctx)

	if took := time.Since(start); took > time.Second {
		t.Errorf("fetch() took %v, want it to stop after its budget", took)
	}
	// Running out of time after storing some is no failure: the rest follows
	// next minute.
	if !fetched || f.failing || f.after != device.minutes[2].Time {
		t.Errorf("fetch() = %v, failing %v, after %d; want true, not failing and after the last minute stored, %d", fetched, f.failing, f.after, device.minutes[2].Time)
	}
}

func TestFetcherLeavesTheMinuteToTheRecorderWhenItStoresNothingInTime(t *testing.T) {
	ctx := context.Background()
	device := newMinutesDevice(time.Now(), 10)
	device.broken = func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }
	agent := device.start(t)
	if _, err := agent.Collect(ctx); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	f := &fetcher{agent: agent, store: openTestStore(t), device: "office-pc", maxEntries: 10, budget: 100 * time.Millisecond}

	fetched := f.fetch(ctx)

	// As when a page is too long to store in time on a slow disk: the recorder
	// stores its own average, and the next fetch asks for a shorter page.
	if fetched || !f.failing || f.after == 0 || f.values != ValuesPerAnswer/2 {
		t.Errorf("fetch() = %v, failing %v, after %d, values %d; want false, failing, after kept and %d values", fetched, f.failing, f.after, f.values, ValuesPerAnswer/2)
	}
	// The device answers in time again.
	device = newMinutesDevice(time.Now(), 10)
	f.agent = device.start(t)
	if _, err := f.agent.Collect(ctx); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if !f.fetch(ctx) || len(device.values) == 0 || device.values[0] != ValuesPerAnswer/2 || f.values != ValuesPerAnswer {
		t.Errorf("next fetch asked for %v values, now %d; want %d, and back to %d once it ran in time", device.values, f.values, ValuesPerAnswer/2, ValuesPerAnswer)
	}
}

func TestFetcherLeavesStoppingToTheProgram(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	device := newMinutesDevice(time.Now(), 10)
	device.broken = func(_ http.ResponseWriter, r *http.Request) {
		stop()
		<-r.Context().Done()
	}
	agent := device.start(t)
	if _, err := agent.Collect(ctx); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	f := &fetcher{agent: agent, store: openTestStore(t), device: "office-pc", maxEntries: 10}

	if !f.fetch(ctx) || f.failing {
		t.Errorf("fetch() while the program stops = false or failing; want true quietly, as the recorder stops too")
	}
}

func TestFetcherFetchesNothingOlderThanTheRetention(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Now()
	device := newMinutesDevice(now, 10)
	agent := device.start(t)
	// The hub was off for longer than it keeps its history.
	own := now.Truncate(time.Minute).Add(-90 * time.Minute)
	if err := store.Add(ctx, "office-pc", own, map[string]float64{history.MetricCPU: 99}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if _, err := agent.Collect(ctx); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	f := &fetcher{agent: agent, store: store, device: "office-pc", maxEntries: 10, retention: 5 * time.Minute}

	f.fetch(ctx)

	if oldest := now.Add(-5*time.Minute).Add(device.clock).Unix() - 1; device.asked[0] < oldest {
		t.Errorf("asked after %d, want after %d at the earliest, the start of the retention on the device's clock", device.asked[0], oldest)
	}
	got := storedTimes(t, store, "office-pc")
	if len(got) < 4 || got[0] != own.Unix() || got[1] < now.Add(-5*time.Minute).Unix() {
		t.Errorf("stored minutes %v, want the hub's own and the ones within the retention", got)
	}
}

func TestFetcherDescribesTheExtrasItFetches(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Now()
	device := newMinutesDevice(now, 3)
	for _, minute := range device.minutes {
		minute.Values["extra:power/cpu"] = 10
		minute.Values["extra:power/gpu"] = 20
	}
	device.extras = map[string]history.ExtraInfo{
		"extra:power/cpu": {Title: "Power", Label: "CPU", Unit: metrics.UnitWatts},
		"extra:power/gpu": {Title: "Power", Label: strings.Repeat("x", 200), Unit: "lumen"},
		"extra:other/one": {Title: "Not among the minutes", Label: "One", Unit: metrics.UnitNumber},
	}
	agent := device.start(t)
	if err := store.Add(ctx, "office-pc", now.Truncate(time.Minute).Add(-3*time.Minute), map[string]float64{history.MetricCPU: 99}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	// The hub knows how one is described already, from the device's readings.
	known := history.ExtraInfo{Title: "Power", Label: "Processor", Unit: metrics.UnitWatts}
	if err := store.SetExtraInfo(ctx, "office-pc", map[string]history.ExtraInfo{"extra:power/cpu": known}, now); err != nil {
		t.Fatalf("SetExtraInfo() error = %v", err)
	}
	if _, err := agent.Collect(ctx); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	f := &fetcher{agent: agent, store: store, device: "office-pc", maxEntries: 10}

	if !f.fetch(ctx) {
		t.Fatal("fetch() = false, want true")
	}

	got, err := store.ExtraInfo(ctx, "office-pc")
	if err != nil || len(got) != 2 || !reflect.DeepEqual(got["extra:power/cpu"], known) {
		t.Fatalf("ExtraInfo() = %+v, %v; want the known one kept and the other added", got, err)
	}
	// Cut down as the extras of a reading are.
	if gpu := got["extra:power/gpu"]; len(gpu.Label) != 80 || gpu.Unit != metrics.UnitNumber {
		t.Errorf("the fetched description = %+v, want its label cut to 80 characters and an unknown unit a number", gpu)
	}
}

func TestKeepKeepsWhatTheRecorderKeeps(t *testing.T) {
	values := map[string]float64{
		"cpu": 1, "memory": 2, "swap": 3, "battery": 4,
		"temperature:b": 5, "temperature:a": 6, "temperature:c": 7,
		"disk:/b": 8, "disk.read:/b": 9, "disk.write:/b": 10, "disk:/a": 11, "disk.read:/c": 12,
		"network.receive:eth0": 13, "network.send:eth0": 14, "network.send:eth1": 15, "network.send:wlan0": 16,
		"gpu:one": 17, "gpu.memory:one": 18,
		"extra:power/cpu": 19, "extra:power/gpu": 20, "extra:power/soc": 21,
		"extra:alpha/one": 22, "extra:zeta/one": 23, "extra:Bad/one": 24,
		"unknown": 25, "fan:one": 26,
	}
	want := map[string]float64{
		"cpu": 1, "memory": 2, "swap": 3, "battery": 4,
		"temperature:a": 6, "temperature:b": 5,
		"disk:/b": 8, "disk.read:/b": 9, "disk.write:/b": 10, "disk:/a": 11,
		"network.receive:eth0": 13, "network.send:eth0": 14, "network.send:eth1": 15,
		"gpu:one": 17, "gpu.memory:one": 18,
		"extra:alpha/one": 22, "extra:power/cpu": 19, "extra:power/gpu": 20,
	}
	// The same every time, not whichever the order of a map picks.
	for range 20 {
		got, dropped := keep(values, 2)
		if !reflect.DeepEqual(got, want) || !dropped {
			t.Fatalf("keep() = %v, %v; want %v, true", got, dropped, want)
		}
	}
	if _, dropped := keep(map[string]float64{"cpu": 1, "disk:/a": 2}, 2); dropped {
		t.Error("keep() of fewer entries than kept dropped some")
	}
}
