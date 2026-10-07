package hub

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/history"
)

// startDevice runs a usage-control that answers with a fixed snapshot and
// returns its address.
func startDevice(t *testing.T) string {
	t.Helper()
	device := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"cpu":{"usagePercent":12.5,"cores":4}}`))
	}))
	t.Cleanup(device.Close)
	return strings.TrimPrefix(device.URL, "http://")
}

func openTestHub(t *testing.T, store *history.Store, fixed []Device) *Hub {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h, err := New(ctx, store, fixed, history.DefaultMaxEntries, 30*24*time.Hour, "9393")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		cancel()
		h.Wait()
	})
	return h
}

func openTestStore(t *testing.T) *history.Store {
	t.Helper()
	store, err := history.Open(context.Background(), filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func ids(remotes []*Remote) []string {
	var ids []string
	for _, r := range remotes {
		ids = append(ids, r.ID)
	}
	return ids
}

func problemOf(err error) Problem {
	var input *InputError
	if errors.As(err, &input) {
		return input.Problem
	}
	return ""
}

func TestAddedDevicesAreKeptAcrossRestarts(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	address := startDevice(t)
	h := openTestHub(t, store, nil)

	device, err := h.Add(ctx, "Office PC", address, KindServer)
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if device.ID != "office-pc" {
		t.Errorf("ID = %q, want office-pc", device.ID)
	}

	again := openTestHub(t, store, nil)
	if got := ids(again.Remotes()); len(got) != 1 || got[0] != "office-pc" {
		t.Errorf("devices after a restart = %q, want [office-pc]", got)
	}
}

func TestAddRefusesDevicesThatCannotBeAdded(t *testing.T) {
	ctx := context.Background()
	address := startDevice(t)
	h := openTestHub(t, openTestStore(t), []Device{{ID: "office-pc", Name: "Office PC", Address: address}})

	tests := []struct {
		name, address string
		want          Problem
	}{
		{"Office PC", address, ProblemNameTaken},
		{"Local", address, ProblemNameTaken},
		{"Laptop", address, ProblemAddressTaken},
		{"Laptop", strings.ToUpper(strings.Replace(address, "127.0.0.1", "[::ffff:127.0.0.1]", 1)), ProblemAddressTaken},
		{"Laptop", "192.168.1.30", ProblemAddress},
		{"--", address, ProblemName},
		{"Laptop", "127.0.0.1:1", ProblemUnreachable},
		{"Laptop", "203.0.113.5:9393", ProblemUnreachable},
	}
	for _, tt := range tests {
		if _, err := h.Add(ctx, tt.name, tt.address, KindServer); problemOf(err) != tt.want {
			t.Errorf("Add(%q, %q) error = %v, want problem %q", tt.name, tt.address, err, tt.want)
		}
	}
}

func TestRemoveForgetsTheDeviceAndItsHistory(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	h := openTestHub(t, store, []Device{{ID: "pi", Name: "Pi", Address: startDevice(t)}})
	for _, name := range []string{"Office PC", "Laptop"} {
		if _, err := h.Add(ctx, name, startDevice(t), KindServer); err != nil {
			t.Fatalf("Add(%q) error = %v", name, err)
		}
	}
	now := time.Now()
	for _, id := range []string{"office-pc", "laptop"} {
		if err := store.Add(ctx, id, now, map[string]float64{history.MetricCPU: 1}); err != nil {
			t.Fatalf("store.Add() error = %v", err)
		}
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

	if got := ids(h.Remotes()); len(got) != 1 || got[0] != "pi" {
		t.Errorf("devices = %q, want only the fixed [pi]", got)
	}
	for id, want := range map[string]int{"office-pc": 0, "laptop": 1} {
		series, err := store.Range(ctx, id, now.Add(-time.Minute), now.Add(time.Minute), time.Minute)
		if err != nil || len(series) != want {
			t.Errorf("history of %s = %+v, %v; want %d series", id, series, err, want)
		}
	}
	if err := h.Remove(ctx, "pi", false); problemOf(err) != ProblemFixed {
		t.Errorf("Remove(pi) error = %v, want problem %q", err, ProblemFixed)
	}
	if err := h.Remove(ctx, "nas", false); problemOf(err) != ProblemNotFound {
		t.Errorf("Remove(nas) error = %v, want problem %q", err, ProblemNotFound)
	}
}

func TestRemoveFinishesWhenTheRequestIsCancelled(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	h := openTestHub(t, store, nil)
	if _, err := h.Add(ctx, "Office PC", startDevice(t), KindServer); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	now := time.Now()
	if err := store.Add(ctx, "office-pc", now, map[string]float64{history.MetricCPU: 1}); err != nil {
		t.Fatalf("store.Add() error = %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()

	if err := h.Remove(cancelled, "office-pc", false); err != nil {
		t.Fatalf("Remove() with a cancelled request error = %v", err)
	}
	if err := h.waitRemoved(ctx, "office-pc", time.Minute); err != nil {
		t.Fatalf("waitRemoved() error = %v", err)
	}

	if got := ids(h.Remotes()); len(got) != 0 {
		t.Errorf("devices = %q, want none", got)
	}
	var count int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM hub_devices`).Scan(&count); err != nil || count != 0 {
		t.Errorf("saved devices = %d, %v; want none", count, err)
	}
	if series, err := store.Range(ctx, "office-pc", now.Add(-time.Minute), now.Add(time.Minute), time.Minute); err != nil || len(series) != 0 {
		t.Errorf("history = %+v, %v; want it deleted", series, err)
	}
}

func TestRemoveDeletesTheDataWithoutHoldingUpThePage(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	h := openTestHub(t, store, nil)
	if _, err := h.Add(ctx, "Office PC", startDevice(t), KindServer); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	now := time.Now()
	if err := store.Add(ctx, "office-pc", now, map[string]float64{history.MetricCPU: 1}); err != nil {
		t.Fatalf("store.Add() error = %v", err)
	}
	deleting, release := make(chan struct{}), make(chan struct{})
	h.beforeDelete = func(string) {
		close(deleting)
		<-release
	}

	if err := h.Remove(ctx, "office-pc", false); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	<-deleting

	// While the data is being deleted, the page is served and other devices
	// can be added, but not one with the same name yet.
	if got := ids(h.Remotes()); len(got) != 0 {
		t.Errorf("devices while deleting = %q, want none", got)
	}
	if _, err := h.Add(ctx, "Laptop", startDevice(t), KindServer); err != nil {
		t.Errorf("Add(Laptop) while deleting error = %v", err)
	}
	// Adding it again waits a moment, then is refused rather than holding up
	// the request until the delete is done.
	started := time.Now()
	if _, err := h.Add(ctx, "Office PC", startDevice(t), KindServer); problemOf(err) != ProblemRemoving {
		t.Errorf("Add(Office PC) while deleting error = %v, want problem %q", err, ProblemRemoving)
	}
	if waited := time.Since(started); waited < removingWait || waited > removingWait+time.Second {
		t.Errorf("Add(Office PC) while deleting took %v, want about %v", waited, removingWait)
	}
	h.mu.Lock()
	err := h.taken(Device{ID: "office-pc", Address: "192.168.1.99:9393"})
	h.mu.Unlock()
	if problemOf(err) != ProblemRemoving {
		t.Errorf("taken(office-pc) while deleting = %v, want problem %q", err, ProblemRemoving)
	}

	close(release)
	if err := h.waitRemoved(ctx, "office-pc", time.Minute); err != nil {
		t.Fatalf("waitRemoved() error = %v", err)
	}
	if series, err := store.Range(ctx, "office-pc", now.Add(-time.Minute), now.Add(time.Minute), time.Minute); err != nil || len(series) != 0 {
		t.Errorf("history = %+v, %v; want it deleted", series, err)
	}
	if _, err := h.Add(ctx, "Office PC", startDevice(t), KindServer); err != nil {
		t.Errorf("Add(Office PC) after deleting error = %v", err)
	}
}

func TestNoChangesOnceTheHubStops(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	running, stop := context.WithCancel(ctx)
	h, err := New(running, store, nil, history.DefaultMaxEntries, 30*24*time.Hour, "9393")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := h.Add(ctx, "Office PC", startDevice(t), KindServer); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	stop()
	h.Wait()

	if _, err := h.Add(ctx, "Laptop", startDevice(t), KindServer); !errors.Is(err, ErrStopping) {
		t.Errorf("Add() once stopped error = %v, want ErrStopping", err)
	}
	if err := h.Remove(ctx, "office-pc", false); !errors.Is(err, ErrStopping) {
		t.Errorf("Remove() once stopped error = %v, want ErrStopping", err)
	}
	if got := ids(h.Remotes()); len(got) != 1 {
		t.Errorf("devices once stopped = %q, want the one added before", got)
	}
}

func TestNewForgetsTheAvailabilityOfDevicesNoLongerCollectedFrom(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	pi := Device{ID: "pi", Name: "Pi", Address: startDevice(t)}
	nas := Device{ID: "nas", Name: "NAS", Address: startDevice(t)}
	first, cancel := context.WithCancel(ctx)
	h, err := New(first, store, []Device{pi, nas}, 0, 30*24*time.Hour, "9393")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := h.Add(ctx, "Laptop", startDevice(t), KindServer); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	now := time.Now()
	for _, id := range []string{"pi", "nas", "laptop"} {
		if _, err := store.DB().Exec(`INSERT INTO hub_outages (device, started, ended) VALUES (?, ?, ?)`, id, now.UnixMilli(), now.UnixMilli()); err != nil {
			t.Fatal(err)
		}
		if err := store.Add(ctx, id, now, map[string]float64{history.MetricCPU: 1}); err != nil {
			t.Fatalf("store.Add() error = %v", err)
		}
	}
	cancel()
	h.Wait()

	// The NAS is no longer in HUB_DEVICES; the laptop is still saved.
	openTestHub(t, store, []Device{pi})

	for id, want := range map[string]int{"pi": 1, "laptop": 1, "nas": 0} {
		var watched, outages int
		if err := store.DB().QueryRow(`SELECT (SELECT COUNT(*) FROM hub_watched WHERE device = ?1), (SELECT COUNT(*) FROM hub_outages WHERE device = ?1)`, id).Scan(&watched, &outages); err != nil {
			t.Fatal(err)
		}
		if watched != want || outages != want {
			t.Errorf("availability rows of %s = %d watched, %d outages; want %d each", id, watched, outages, want)
		}
		// The history ages out with the retention instead.
		if series, err := store.Range(ctx, id, now.Add(-time.Minute), now.Add(time.Minute), time.Minute); err != nil || len(series) != 1 {
			t.Errorf("history of %s = %+v, %v; want it kept", id, series, err)
		}
	}
}

func TestKindIsKeptAndCanBeChanged(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	pi := Device{ID: "pi", Name: "Pi", Address: startDevice(t)}
	h := openTestHub(t, store, []Device{pi})
	if _, err := h.Add(ctx, "Laptop", startDevice(t), KindPC); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if _, err := h.Add(ctx, "Tablet", startDevice(t), "phone"); problemOf(err) != ProblemKind {
		t.Errorf("Add() with an unknown kind error = %v, want problem %q", err, ProblemKind)
	}
	if err := h.SetKind(ctx, "pi", KindPC); err != nil {
		t.Fatalf("SetKind(pi) error = %v", err)
	}
	if err := h.SetKind(ctx, "laptop", KindServer); err != nil {
		t.Fatalf("SetKind(laptop) error = %v", err)
	}
	if err := h.SetKind(ctx, "nas", KindPC); problemOf(err) != ProblemNotFound {
		t.Errorf("SetKind(nas) error = %v, want problem %q", err, ProblemNotFound)
	}

	again := openTestHub(t, store, []Device{pi})
	want := map[string]Kind{"pi": KindPC, "laptop": KindServer}
	for _, remote := range again.Remotes() {
		if remote.Kind() != want[remote.ID] {
			t.Errorf("kind of %s after a restart = %q, want %q", remote.ID, remote.Kind(), want[remote.ID])
		}
		availability, err := remote.Availability(ctx)
		if err != nil || availability.Kind != want[remote.ID] {
			t.Errorf("Availability(%s) = %+v, %v; want kind %q", remote.ID, availability, err, want[remote.ID])
		}
	}
}

func TestRemoveForgetsTheKind(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	h := openTestHub(t, store, nil)
	if _, err := h.Add(ctx, "Laptop", startDevice(t), KindPC); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if err := h.Remove(ctx, "laptop", false); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if err := h.waitRemoved(ctx, "laptop", time.Minute); err != nil {
		t.Fatalf("waitRemoved() error = %v", err)
	}
	if kind, err := readKind(ctx, store.DB(), "laptop"); err != nil || kind != KindServer {
		t.Errorf("kind after removing = %q, %v; want none stored", kind, err)
	}
}

func TestKeepingTheHistoryKeepsAvailabilityAndKindAcrossRestarts(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	h := openTestHub(t, store, nil)
	if _, err := h.Add(ctx, "Laptop", startDevice(t), KindPC); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	now := time.Now()
	if _, err := store.DB().Exec(`INSERT INTO hub_outages (device, started, ended) VALUES ('laptop', ?, ?)`, now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := h.Remove(ctx, "laptop", true); err != nil {
		t.Fatalf("Remove(keepHistory) error = %v", err)
	}
	if err := h.waitRemoved(ctx, "laptop", time.Minute); err != nil {
		t.Fatalf("waitRemoved() error = %v", err)
	}

	again := openTestHub(t, store, nil)
	if kind, err := readKind(ctx, store.DB(), "laptop"); err != nil || kind != KindPC {
		t.Errorf("kind after a restart = %q, %v; want %q kept", kind, err, KindPC)
	}
	if availability, err := readAvailability(ctx, store.DB(), "laptop"); err != nil || availability.Outages != 1 {
		t.Errorf("availability after a restart = %+v, %v; want the outage kept", availability, err)
	}

	// Added again, the device continues them and is no longer listed as kept.
	if _, err := again.Add(ctx, "Laptop", startDevice(t), KindPC); err != nil {
		t.Fatalf("Add() again error = %v", err)
	}
	if kept, err := again.keptHistory(ctx); err != nil || len(kept) != 0 {
		t.Errorf("kept devices after adding again = %q, %v; want none", kept, err)
	}
	if availability, err := readAvailability(ctx, store.DB(), "laptop"); err != nil || availability.Outages != 1 {
		t.Errorf("availability after adding again = %+v, %v; want the outage continued", availability, err)
	}
}

func TestAnEmptyKindIsAServer(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	h := openTestHub(t, store, nil)
	if _, err := h.Add(ctx, "NAS", startDevice(t), ""); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if err := h.SetKind(ctx, "nas", ""); err != nil {
		t.Fatalf("SetKind() error = %v", err)
	}
	for _, remote := range openTestHub(t, store, nil).Remotes() {
		if remote.Kind() != KindServer {
			t.Errorf("kind of %s = %q, want %q", remote.ID, remote.Kind(), KindServer)
		}
	}
	var rows int
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM hub_device_kinds`).Scan(&rows); err != nil || rows != 0 {
		t.Errorf("hub_device_kinds has %d rows (%v), want none for a server", rows, err)
	}
}

func TestASavedDeviceAtAnAddressInUseIsSkipped(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	address := startDevice(t)
	openTestHub(t, store, nil) // creates the tables
	// Added on the page before each address could be added only once.
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO hub_devices (id, name, address, added) VALUES ('pc', 'PC', ?, 0)`, address); err != nil {
		t.Fatal(err)
	}
	h := openTestHub(t, store, []Device{{ID: "office-pc", Name: "Office PC", Address: address}})
	if got := ids(h.Remotes()); len(got) != 1 || got[0] != "office-pc" {
		t.Errorf("devices = %q, want only office-pc from HUB_DEVICES", got)
	}
}
