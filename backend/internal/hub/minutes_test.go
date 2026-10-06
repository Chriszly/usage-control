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
// kept a minute for every one of the last ten, as while the hub could not
// reach it. It hands them out three per answer and remembers what it was
// asked.
type minutesDevice struct {
	clock   time.Duration
	minutes []history.Minute
	asked   []int64
}

func newMinutesDevice(now time.Time) *minutesDevice {
	d := &minutesDevice{clock: -time.Hour}
	deviceNow := now.Add(d.clock).Truncate(time.Minute)
	for i := 10; i >= 1; i-- {
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
			_, _ = fmt.Fprintf(w, `{"time":%q,"cpu":{"usagePercent":1}}`, now.UTC().Format(time.RFC3339))
		case MinutesPath:
			after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
			d.asked = append(d.asked, after)
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

func TestFetcherFillsTheGapFromTheMinutesTheDeviceKept(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Now()
	device := newMinutesDevice(now)
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
	device := newMinutesDevice(time.Now())
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
	device := newMinutesDevice(time.Now())
	agent := device.start(t)
	f := &fetcher{agent: agent, store: openTestStore(t), device: "office-pc", maxValues: 10}

	if !f.fetch(ctx) || len(device.asked) != 0 {
		t.Errorf("fetch() before the device answered asked %d times; want true without asking", len(device.asked))
	}
}

func TestFetcherCleansTheMinutesOfTheDevice(t *testing.T) {
	f := &fetcher{after: 1000, maxValues: 2}
	answer := MinutesAnswer{Now: time.Now().Unix(), Minutes: []history.Minute{
		{Time: 900, Values: map[string]float64{"cpu": 1}},                      // the hub has it
		{Time: 1060, Values: map[string]float64{"cpu": 2, "bad": math.NaN()}},  // NaN dropped
		{Time: 1061, Values: map[string]float64{"cpu": 3}},                     // too close to the one before
		{Time: 1120, Values: map[string]float64{strings.Repeat("x", 300): 4}},  // only a too long name
		{Time: 1180, Values: map[string]float64{"a": 1, "b": 2, "c": 3}},       // too many values
		{Time: time.Now().Unix() + 3600, Values: map[string]float64{"cpu": 5}}, // in the device's future
	}}

	got, after := f.clean(answer)

	offset := time.Now().Unix() - answer.Now
	if len(got) != 2 || got[0].Time != 1060+offset || len(got[0].Values) != 1 || got[1].Time != 1180+offset || len(got[1].Values) != 2 {
		t.Errorf("clean() = %+v, want the minutes at 1060 with only cpu and at 1180 with two values", got)
	}
	if after != 1180 || f.after != 1000 {
		t.Errorf("after = %d, fetcher's after %d; want 1180, the last minute taken, and the fetcher's unchanged until they are stored", after, f.after)
	}
}
