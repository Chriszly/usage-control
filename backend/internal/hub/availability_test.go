package hub

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// startSwitchableDevice runs a usage-control that answers while up is set.
func startSwitchableDevice(t *testing.T, up *atomic.Bool) string {
	t.Helper()
	device := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !up.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"cpu":{"usagePercent":12.5,"cores":4}}`))
	}))
	t.Cleanup(device.Close)
	return strings.TrimPrefix(device.URL, "http://")
}

func TestWatchedAgentNotesOutages(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	openTestHub(t, store, nil) // creates the tables
	var up atomic.Bool
	up.Store(true)
	since := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if err := watch(ctx, store.DB(), "pi", since); err != nil {
		t.Fatalf("watch() error = %v", err)
	}
	agent := &watchedAgent{agent: NewAgent(startSwitchableDevice(t, &up)), db: store.DB(), device: "pi"}
	collect := func(answers bool) {
		t.Helper()
		up.Store(answers)
		if _, err := agent.Collect(ctx); (err == nil) != answers {
			t.Fatalf("Collect() error = %v, want answered %v", err, answers)
		}
	}

	collect(true)
	got, err := readAvailability(ctx, store.DB(), "pi")
	if err != nil || got.Outages != 0 || got.LastOutage != nil || !got.Since.Equal(since) {
		t.Fatalf("availability before an outage = %+v, %v; want none since %v", got, err, since)
	}

	// A single failed reading is no outage.
	collect(false)
	collect(true)
	got, err = readAvailability(ctx, store.DB(), "pi")
	if err != nil || got.Outages != 0 {
		t.Fatalf("availability after one failed reading = %+v, %v; want no outage", got, err)
	}

	collect(false)
	collect(false)
	collect(true)
	collect(true)
	collect(false)
	collect(false)
	got, err = readAvailability(ctx, store.DB(), "pi")
	if err != nil || got.Outages != 2 || got.LastOutage == nil || got.LastOutage.End.Before(got.LastOutage.Start) {
		t.Fatalf("availability = %+v, %v; want 2 outages, the last one ongoing", got, err)
	}
	if got.OfflineSeconds < 0 || got.OfflineSeconds > 10 {
		t.Errorf("offline = %d s, want the few seconds the test took", got.OfflineSeconds)
	}

	// Watching again keeps the first time.
	if err := watch(ctx, store.DB(), "pi", time.Now()); err != nil {
		t.Fatalf("watch() again error = %v", err)
	}
	if got, _ := readAvailability(ctx, store.DB(), "pi"); !got.Since.Equal(since) {
		t.Errorf("since after watching again = %v, want %v", got.Since, since)
	}
}

func TestWatchedAgentIgnoresTheHubStopping(t *testing.T) {
	store := openTestStore(t)
	openTestHub(t, store, nil)
	var up atomic.Bool
	agent := &watchedAgent{agent: NewAgent(startSwitchableDevice(t, &up)), db: store.DB(), device: "pi"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _ = agent.Collect(ctx)

	if got, err := readAvailability(context.Background(), store.DB(), "pi"); err != nil || got.Outages != 0 {
		t.Errorf("availability = %+v, %v; want no outage", got, err)
	}
}

func TestWatchedAgentCountsNoOutageWhenTheHubRefusesTheAddress(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	openTestHub(t, store, nil)
	var up, refuse atomic.Bool
	agent := newAgent(startSwitchableDevice(t, &up), func(string, string, syscall.RawConn) error {
		if refuse.Load() {
			return errOwnAddress
		}
		return nil
	})
	began := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if err := watch(ctx, store.DB(), "pi", began); err != nil {
		t.Fatalf("watch() error = %v", err)
	}
	now := began
	watched := &watchedAgent{agent: agent, db: store.DB(), device: "pi", clock: func() time.Time { return now }}
	remote := &Remote{Agent: agent, watched: watched}
	var logged bytes.Buffer
	defer slog.SetDefault(slog.Default())
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	// collect reads once more, 5 s later, over a new connection, which the
	// hub checks.
	// collect reads once and expects an error: want, or any error when nil.
	collect := func(want error) {
		t.Helper()
		now = now.Add(5 * time.Second)
		agent.client.CloseIdleConnections()
		_, err := watched.Collect(ctx)
		if want != nil && !errors.Is(err, want) || want == nil && err == nil {
			t.Fatalf("Collect() error = %v, want %v", err, want)
		}
	}

	// The device does not answer, then its address turns out to be the hub's own.
	collect(nil)
	collect(nil)
	lastFailed := now
	refuse.Store(true)
	for range 3 {
		collect(errOwnAddress)
	}

	got, err := readAvailability(ctx, store.DB(), "pi")
	if err != nil || got.Outages != 1 || got.LastOutage == nil || !got.LastOutage.End.Equal(lastFailed) {
		t.Fatalf("availability = %+v, %v; want 1 outage ending at the last failed reading %v", got, err, lastFailed)
	}
	if _, _, ok := watched.ongoing(); ok {
		t.Error("ongoing() = true, want the outage ended")
	}
	if since, unreachable := remote.Unreachable(); !unreachable || !since.IsZero() {
		t.Errorf("Unreachable() = %v, %v; want not answering, with no outage", since, unreachable)
	}
	if n := strings.Count(logged.String(), "HUB_DEVICES under the same name"); n != 1 {
		t.Errorf("logged the refusal %d times, want once:\n%s", n, logged.String())
	}

	// Once the address is not refused, a device that does not answer has an
	// outage again.
	refuse.Store(false)
	collect(nil)
	collect(nil)
	got, err = readAvailability(ctx, store.DB(), "pi")
	if err != nil || got.Outages != 2 || got.LastOutage == nil || !got.LastOutage.Start.After(lastFailed) {
		t.Errorf("availability = %+v, %v; want a second outage after %v", got, err, lastFailed)
	}
}

func TestAvailabilityIsKeptAndRemovedWithTheDevice(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	h := openTestHub(t, store, nil)
	before := time.Now().Add(-time.Second)
	for _, name := range []string{"Office PC", "Laptop"} {
		if _, err := h.Add(ctx, name, startDevice(t), KindServer, nil); err != nil {
			t.Fatalf("Add(%q) error = %v", name, err)
		}
	}
	remote := h.Remotes()[0]
	got, err := remote.Availability(ctx)
	if err != nil || got.Since.Before(before.Truncate(time.Second)) || got.Since.After(time.Now()) || got.Outages != 0 {
		t.Fatalf("availability of %s = %+v, %v; want none since it was added", remote.ID, got, err)
	}

	if err := h.Remove(ctx, "office-pc", false); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if err := h.Remove(ctx, "laptop", true); err != nil {
		t.Fatalf("Remove(keepHistory) error = %v", err)
	}
	for _, id := range []string{"office-pc", "laptop"} {
		if err := h.waitRemoved(ctx, id, time.Minute); err != nil {
			t.Fatalf("waitRemoved(%q) error = %v", id, err)
		}
	}
	for id, want := range map[string]int{"office-pc": 0, "laptop": 1} {
		var count int
		if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM hub_watched WHERE device = ?`, id).Scan(&count); err != nil || count != want {
			t.Errorf("watched rows of %s = %d, %v; want %d", id, count, err, want)
		}
	}
}

func TestRemoteTellsSinceWhenItIsUnreachable(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	openTestHub(t, store, nil) // creates the tables
	var up atomic.Bool
	agent := NewAgent(startSwitchableDevice(t, &up))
	remote := &Remote{Agent: agent, watched: &watchedAgent{agent: agent, db: store.DB(), device: "pi"}}
	if since, unreachable := remote.Unreachable(); !unreachable || !since.IsZero() {
		t.Errorf("before asking: Unreachable() = %v, %v; want unreachable since an unknown time", since, unreachable)
	}

	before := time.Now()
	_, _ = remote.watched.Collect(ctx)
	_, _ = remote.watched.Collect(ctx)
	if since, unreachable := remote.Unreachable(); !unreachable || since.Before(before) || since.After(time.Now()) {
		t.Errorf("after failing: Unreachable() = %v, %v; want unreachable since the first failure", since, unreachable)
	}

	up.Store(true)
	_, _ = remote.watched.Collect(ctx)
	if _, unreachable := remote.Unreachable(); unreachable {
		t.Error("after answering: Unreachable() = true, want false")
	}
}

func TestWatchedAgentWritesAnOngoingOutageEveryFiveMinutes(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	openTestHub(t, store, nil) // creates the tables
	var up atomic.Bool
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	agent := &watchedAgent{agent: NewAgent(startSwitchableDevice(t, &up)), db: store.DB(), device: "pi", clock: func() time.Time { return now }}
	remote := &Remote{Device: Device{ID: "pi"}, db: store.DB(), watched: agent}
	if err := watch(ctx, store.DB(), "pi", now); err != nil {
		t.Fatalf("watch() error = %v", err)
	}
	collect := func(answers bool) {
		t.Helper()
		up.Store(answers)
		if _, err := agent.Collect(ctx); (err == nil) != answers {
			t.Fatalf("Collect() error = %v, want answered %v", err, answers)
		}
	}
	written := func() (started, ended time.Time) {
		t.Helper()
		var s, e int64
		if err := store.DB().QueryRow(`SELECT started, ended FROM hub_outages WHERE device = 'pi' ORDER BY started DESC LIMIT 1`).Scan(&s, &e); err != nil {
			t.Fatalf("read the outage: %v", err)
		}
		return time.UnixMilli(s).UTC(), time.UnixMilli(e).UTC()
	}

	start := now
	collect(false)
	if _, _, ongoing := agent.ongoing(); ongoing {
		t.Error("ongoing() = true after one failed reading, want false")
	}
	now = now.Add(5 * time.Second)
	collect(false)
	if s, e := written(); !s.Equal(start) || !e.Equal(now) {
		t.Errorf("outage after the second failed reading = %v to %v, want %v to %v", s, e, start, now)
	}

	// Within the next five minutes, the failed readings are not written, but
	// the page gets the current end from memory.
	noted := now
	now = now.Add(5 * time.Second)
	collect(false)
	if s, e := written(); !s.Equal(start) || !e.Equal(noted) {
		t.Errorf("outage 5 s later = %v to %v, want it unchanged in the database", s, e)
	}
	got, err := remote.Availability(ctx)
	if err != nil || got.Outages != 1 || got.OfflineSeconds != 10 || got.LastOutage == nil || !got.LastOutage.End.Equal(now) {
		t.Errorf("availability 5 s later = %+v, %v; want 1 outage of 10 s ending now", got, err)
	}

	now = now.Add(5 * time.Minute)
	collect(false)
	if _, e := written(); !e.Equal(now) {
		t.Errorf("outage after five minutes ends at %v in the database, want %v", e, now)
	}

	now = now.Add(5 * time.Second)
	collect(true)
	if _, e := written(); !e.Equal(now) {
		t.Errorf("outage after the device answers ends at %v, want %v", e, now)
	}
	if _, _, ongoing := agent.ongoing(); ongoing {
		t.Error("ongoing() = true after the device answered, want false")
	}
	got, err = remote.Availability(ctx)
	if err != nil || got.Outages != 1 || got.OfflineSeconds != 5*60+15 || got.LastOutage == nil || !got.LastOutage.End.Equal(now) {
		t.Errorf("availability after the outage = %+v, %v; want 1 outage of 5 min 15 s", got, err)
	}
}
