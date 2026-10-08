package hub

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
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
	h, err := newHub(ctx, store, fixed, history.DefaultMaxEntries, 30*24*time.Hour, "9393", true)
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

	device, err := h.Add(ctx, "Office PC", address, KindServer, nil)
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

func TestTheHubsIDIsMadeOnceAndKept(t *testing.T) {
	store := openTestStore(t)
	h := openTestHub(t, store, []Device{{ID: "office-pc", Name: "Office PC", Address: startDevice(t)}})
	if !ValidHubID(h.id) || h.Remotes()[0].Agent.hubID != h.id {
		t.Fatalf("id = %q, the agent's %q; want a valid id told to every device", h.id, h.Remotes()[0].Agent.hubID)
	}
	if again := openTestHub(t, store, nil); again.id != h.id {
		t.Errorf("id after a restart = %q, want the same %q", again.id, h.id)
	}
	if other := openTestHub(t, openTestStore(t), nil); other.id == h.id {
		t.Errorf("another hub has the same id %q, want its own", other.id)
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
		if _, err := h.Add(ctx, tt.name, tt.address, KindServer, nil); problemOf(err) != tt.want {
			t.Errorf("Add(%q, %q) error = %v, want problem %q", tt.name, tt.address, err, tt.want)
		}
	}
}

func TestAddRefusesTheHubsOwnAddresses(t *testing.T) {
	ctx := context.Background()
	address := startDevice(t)
	_, port, _ := net.SplitHostPort(address)
	h := openTestHub(t, openTestStore(t), nil)
	h.allowLoopback = false
	h.suggester.ownAddrs = func() ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("192.168.1.9"), Mask: net.CIDRMask(24, 32)}}, nil
	}
	own := []netip.Addr{netip.MustParseAddr("192.168.1.10")}
	// In a container on Docker's bridge network, the host is the bridge's
	// gateway, and has more virtual interfaces, such as a VPN's.
	h.hostAddrs = func() []netip.Addr {
		return []netip.Addr{netip.MustParseAddr("172.18.0.1"), netip.MustParseAddr("10.8.0.2")}
	}

	// Refused at once, however the port, so how long it takes tells nothing.
	for _, address := range []string{
		address, "localhost:" + port, "[::1]:9393", "0.0.0.0:9393", "[::]:9393",
		"[fe80::1]:9393", "192.168.1.9:9393", "[::ffff:192.168.1.9]:9393", "192.168.1.10:9393",
		"172.18.0.1:22", "10.8.0.2:9393",
	} {
		began := time.Now()
		if _, err := h.Add(ctx, "Laptop", address, KindServer, own); problemOf(err) != ProblemAddressOwn {
			t.Errorf("Add(%q) error = %v, want problem %q", address, err, ProblemAddressOwn)
		}
		if took := time.Since(began); took > time.Second {
			t.Errorf("Add(%q) took %v, want an answer at once", address, took)
		}
	}
	// Another address on the local network is connected to as before, which
	// is checked without connecting, so the test sends nothing to the
	// network it runs in.
	check := h.refuseOwn(func() []netip.Addr { return own })
	if err := check("tcp4", "192.168.1.30:9393", nil); err != nil {
		t.Errorf("connecting to 192.168.1.30:9393 error = %v, want none", err)
	}
	if err := check("tcp4", "192.168.1.10:9393", nil); !errors.Is(err, errOwnAddress) {
		t.Errorf("connecting to 192.168.1.10:9393 error = %v, want errOwnAddress", err)
	}
	// A device cabled straight to the hub, with a link-local IPv4 address,
	// can be added too.
	for _, a := range []string{"192.168.1.30", "fd00::30", "169.254.1.1", "172.18.0.5"} {
		if !h.addable(netip.MustParseAddr(a), own) {
			t.Errorf("addable(%s) = false, want another device on the network addable", a)
		}
	}
	if len(h.Remotes()) != 0 {
		t.Errorf("devices = %q, want none added", ids(h.Remotes()))
	}
}

func TestDevicesAddedOnThePageAreNotCollectedFromAtTheHubsOwnAddress(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	added, fixed := startDevice(t), startDevice(t)
	openTestHub(t, store, nil) // creates the tables
	// Added on the page while its host name resolved to another device, and
	// now resolving to the hub itself, as loopback is here.
	if _, err := store.DB().ExecContext(ctx, `INSERT INTO hub_devices (id, name, address, added) VALUES ('laptop', 'Laptop', ?, 0)`, added); err != nil {
		t.Fatal(err)
	}
	var logged bytes.Buffer
	defer slog.SetDefault(slog.Default())
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	running, stop := context.WithCancel(ctx)
	h, err := newHub(running, store, []Device{{ID: "vm", Name: "VM", Address: fixed}}, history.DefaultMaxEntries, 30*24*time.Hour, "9393", false)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		stop()
		h.Wait()
	})

	for _, remote := range h.Remotes() {
		_, err := remote.Agent.Collect(ctx)
		switch remote.ID {
		case "laptop":
			if !errors.Is(err, errOwnAddress) {
				t.Errorf("Collect() from the device added on the page error = %v, want errOwnAddress", err)
			}
		case "vm":
			// HUB_DEVICES may name the hub itself, as for a VM behind port
			// forwarding on it.
			if err != nil {
				t.Errorf("Collect() from the device in HUB_DEVICES error = %v, want none", err)
			}
		}
	}
	if got := ids(h.Remotes()); len(got) != 2 {
		t.Errorf("devices = %q, want vm and laptop", got)
	}
	// The log tells how to keep collecting from it.
	stop()
	h.Wait()
	if text := logged.String(); strings.Count(text, "HUB_DEVICES under the same name") != 1 || !strings.Contains(text, "name=Laptop") {
		t.Errorf("log = %q, want one warning about Laptop", text)
	}
}

func TestRemoveForgetsTheDeviceAndItsHistory(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	h := openTestHub(t, store, []Device{{ID: "pi", Name: "Pi", Address: startDevice(t)}})
	for _, name := range []string{"Office PC", "Laptop"} {
		if _, err := h.Add(ctx, name, startDevice(t), KindServer, nil); err != nil {
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
	if _, err := h.Add(ctx, "Office PC", startDevice(t), KindServer, nil); err != nil {
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
	if _, err := h.Add(ctx, "Office PC", startDevice(t), KindServer, nil); err != nil {
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
	if _, err := h.Add(ctx, "Laptop", startDevice(t), KindServer, nil); err != nil {
		t.Errorf("Add(Laptop) while deleting error = %v", err)
	}
	// Adding it again waits a moment, then is refused rather than holding up
	// the request until the delete is done.
	started := time.Now()
	if _, err := h.Add(ctx, "Office PC", startDevice(t), KindServer, nil); problemOf(err) != ProblemRemoving {
		t.Errorf("Add(Office PC) while deleting error = %v, want problem %q", err, ProblemRemoving)
	}
	// It gives up after removingWait, not once the data is deleted; the room
	// above that is for a slow machine running the tests.
	if waited := time.Since(started); waited < removingWait || waited > removingWait+5*time.Second {
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
	if _, err := h.Add(ctx, "Office PC", startDevice(t), KindServer, nil); err != nil {
		t.Errorf("Add(Office PC) after deleting error = %v", err)
	}
}

func TestNoChangesOnceTheHubStops(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	running, stop := context.WithCancel(ctx)
	h, err := newHub(running, store, nil, history.DefaultMaxEntries, 30*24*time.Hour, "9393", true)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := h.Add(ctx, "Office PC", startDevice(t), KindServer, nil); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	stop()
	h.Wait()

	if _, err := h.Add(ctx, "Laptop", startDevice(t), KindServer, nil); !errors.Is(err, ErrStopping) {
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
	h, err := newHub(first, store, []Device{pi, nas}, 0, 30*24*time.Hour, "9393", true)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := h.Add(ctx, "Laptop", startDevice(t), KindServer, nil); err != nil {
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
	if _, err := h.Add(ctx, "Laptop", startDevice(t), KindPC, nil); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if _, err := h.Add(ctx, "Tablet", startDevice(t), "phone", nil); problemOf(err) != ProblemKind {
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
	if _, err := h.Add(ctx, "Laptop", startDevice(t), KindPC, nil); err != nil {
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
	if _, err := h.Add(ctx, "Laptop", startDevice(t), KindPC, nil); err != nil {
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

	// Added again a day after it was removed, the device continues them, and
	// the day it was removed does not count as watched.
	var sinceBefore int64
	if err := store.DB().QueryRow(`SELECT since FROM hub_watched WHERE device = 'laptop'`).Scan(&sinceBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`UPDATE hub_kept SET removed = removed - 86400`); err != nil {
		t.Fatal(err)
	}
	if _, err := again.Add(ctx, "Laptop", startDevice(t), KindPC, nil); err != nil {
		t.Fatalf("Add() again error = %v", err)
	}
	if kept, err := again.keptHistory(ctx); err != nil || len(kept) != 0 {
		t.Errorf("kept devices after adding again = %q, %v; want none", kept, err)
	}
	if availability, err := readAvailability(ctx, store.DB(), "laptop"); err != nil || availability.Outages != 1 {
		t.Errorf("availability after adding again = %+v, %v; want the outage continued", availability, err)
	}
	var since int64
	if err := store.DB().QueryRow(`SELECT since FROM hub_watched WHERE device = 'laptop'`).Scan(&since); err != nil {
		t.Fatal(err)
	}
	if gap := since - sinceBefore; gap < 86400 || gap > 86400+5 {
		t.Errorf("since moved on by %d s after adding again, want the day it was removed", gap)
	}
	// The page counts the share from the moved on since, and names the
	// outage from before it was removed as counted since its start.
	availability, err := readAvailability(ctx, store.DB(), "laptop")
	if err != nil || availability.CountedSince.Unix() != since || availability.Since.Unix() != now.Unix() {
		t.Errorf("availability after adding again = %+v, %v; want counted since %d and since the outage at %d", availability, err, since, now.Unix())
	}
}

func TestAKeptDeviceInHubDevicesIsCollectedFromAgain(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	h := openTestHub(t, store, nil)
	if _, err := h.Add(ctx, "Laptop", startDevice(t), KindPC, nil); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if err := h.Remove(ctx, "laptop", true); err != nil {
		t.Fatalf("Remove(keepHistory) error = %v", err)
	}
	if err := h.waitRemoved(ctx, "laptop", time.Minute); err != nil {
		t.Fatalf("waitRemoved() error = %v", err)
	}
	// Removed longer than the retention ago, then set in HUB_DEVICES.
	if _, err := store.DB().Exec(`UPDATE hub_kept SET removed = removed - 31*86400`); err != nil {
		t.Fatal(err)
	}
	var sinceBefore int64
	if err := store.DB().QueryRow(`SELECT since FROM hub_watched WHERE device = 'laptop'`).Scan(&sinceBefore); err != nil {
		t.Fatal(err)
	}

	again := openTestHub(t, store, []Device{{ID: "laptop", Name: "Laptop", Address: startDevice(t)}})
	if err := again.forgetExpired(ctx, time.Now()); err != nil {
		t.Fatalf("forgetExpired() error = %v", err)
	}

	if kept, err := again.keptHistory(ctx); err != nil || len(kept) != 0 {
		t.Errorf("kept devices = %q, %v; want none once in HUB_DEVICES", kept, err)
	}
	if kind, err := readKind(ctx, store.DB(), "laptop"); err != nil || kind != KindPC {
		t.Errorf("kind = %q, %v; want %q kept", kind, err, KindPC)
	}
	var since int64
	if err := store.DB().QueryRow(`SELECT since FROM hub_watched WHERE device = 'laptop'`).Scan(&since); err != nil {
		t.Fatalf("the device's availability is gone: %v", err)
	}
	if gap := since - sinceBefore; gap < 31*86400 || gap > 31*86400+5 {
		t.Errorf("since moved on by %d s, want the 31 days it was removed", gap)
	}
}

func TestKeptDevicesAreForgottenAfterTheRetention(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	h := openTestHub(t, store, nil)
	for _, name := range []string{"Laptop", "Tablet"} {
		if _, err := h.Add(ctx, name, startDevice(t), KindPC, nil); err != nil {
			t.Fatalf("Add(%q) error = %v", name, err)
		}
		id := strings.ToLower(name)
		if err := h.Remove(ctx, id, true); err != nil {
			t.Fatalf("Remove(%q) error = %v", id, err)
		}
		if err := h.waitRemoved(ctx, id, time.Minute); err != nil {
			t.Fatalf("waitRemoved() error = %v", err)
		}
	}
	// The laptop was removed 31 days ago, longer than the retention of 30.
	if _, err := store.DB().Exec(`UPDATE hub_kept SET removed = removed - 31*86400 WHERE device = 'laptop'`); err != nil {
		t.Fatal(err)
	}
	if err := h.forgetExpired(ctx, time.Now()); err != nil {
		t.Fatalf("forgetExpired() error = %v", err)
	}
	for id, want := range map[string]int{"laptop": 0, "tablet": 1} {
		var kept, watched, kinds int
		if err := store.DB().QueryRow(`SELECT (SELECT COUNT(*) FROM hub_kept WHERE device = ?1), (SELECT COUNT(*) FROM hub_watched WHERE device = ?1), (SELECT COUNT(*) FROM hub_device_kinds WHERE device = ?1)`, id).Scan(&kept, &watched, &kinds); err != nil {
			t.Fatal(err)
		}
		if kept != want || watched != want || kinds != want {
			t.Errorf("rows of %s = %d kept, %d watched, %d kinds; want %d each", id, kept, watched, kinds, want)
		}
	}
}

func TestAnEmptyKindIsAServer(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	h := openTestHub(t, store, nil)
	if _, err := h.Add(ctx, "NAS", startDevice(t), "", nil); err != nil {
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
