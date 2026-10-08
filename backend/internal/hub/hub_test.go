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
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/history"
	"github.com/Chriszly/usage-control/backend/internal/metrics"
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
	h, err := newHub(ctx, store, fixed, history.DefaultMaxEntries, 30*24*time.Hour, "9393", nil, true)
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
	// A usage-control asked by a name it does not know, as one in Docker by
	// the machine's hostname, answers 421; any other answer than 200 OK
	// tells nothing.
	answering := func(status int) string {
		device := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, http.StatusText(status), status)
		}))
		t.Cleanup(device.Close)
		return strings.TrimPrefix(device.URL, "http://")
	}

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
		{"Laptop", answering(http.StatusMisdirectedRequest), ProblemHostUnknown},
		{"Laptop", answering(http.StatusForbidden), ProblemUnreachable},
		{"Laptop", answering(http.StatusNotFound), ProblemUnreachable},
	}
	for _, tt := range tests {
		if _, err := h.Add(ctx, tt.name, tt.address, KindServer); problemOf(err) != tt.want {
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
	// The machine's network card, which its usage lists.
	h.local = &countingUsage{addresses: []string{"192.168.1.10"}}
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
		if _, err := h.Add(ctx, "Laptop", address, KindServer); problemOf(err) != ProblemAddressOwn {
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

// countingUsage is the hub's own usage, with a network card at addresses,
// and counts how often it was read. While failing is set, reading it fails.
// mu guards addresses.
type countingUsage struct {
	mu        sync.Mutex
	addresses []string
	reads     atomic.Int32
	failing   atomic.Bool
}

// setAddresses gives the network card other addresses.
func (u *countingUsage) setAddresses(addresses ...string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.addresses = addresses
}

func (u *countingUsage) Collect(context.Context) (metrics.Snapshot, error) {
	u.reads.Add(1)
	if u.failing.Load() {
		return metrics.Snapshot{}, errors.New("cannot read the usage")
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	return metrics.Snapshot{Network: []metrics.NetworkInterface{{Name: "eth0", Addresses: u.addresses}}}, nil
}

func TestCollectingRefusesTheSameOwnAddressesAsAdding(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	// In Docker, the machine's network cards are not the container's; its
	// usage lists them.
	usage := &countingUsage{addresses: []string{"192.168.1.20"}}
	h, err := newHub(ctx, openTestStore(t), nil, history.DefaultMaxEntries, 30*24*time.Hour, "9393", usage, false)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		cancel()
		h.Wait()
	})
	h.suggester.ownAddrs = func() ([]net.Addr, error) { return nil, nil }
	hostReads := 0
	h.hostAddrs = func() []netip.Addr {
		hostReads++
		return []netip.Addr{netip.MustParseAddr("172.18.0.1")}
	}

	// Adding refuses the address, also when the page's request gives none.
	if _, err := h.Add(ctx, "Laptop", "192.168.1.20:9393", KindServer); problemOf(err) != ProblemAddressOwn {
		t.Errorf("Add(192.168.1.20:9393) error = %v, want problem %q", err, ProblemAddressOwn)
	}
	// So does collecting, as for a host name that resolves to it later.
	check := h.refuseOwn(h.ownAddresses)
	for _, address := range []string{"192.168.1.20:9393", "172.18.0.1:9393"} {
		if err := check("tcp4", address, nil); !errors.Is(err, errOwnAddress) {
			t.Errorf("connecting to %s error = %v, want errOwnAddress", address, err)
		}
	}
	if err := check("tcp4", "192.168.1.30:9393", nil); err != nil {
		t.Errorf("connecting to 192.168.1.30:9393 error = %v, want none", err)
	}
	if h.addableAddress("192.168.1.20:9393", h.ownAddresses()) {
		t.Error("addableAddress(192.168.1.20:9393) = true, want the hub's own refused")
	}

	// A device that does not answer is connected to every few seconds; the
	// hub's addresses are read again only once they are a few seconds old.
	if n := usage.reads.Load(); n != 1 || hostReads != 1 {
		t.Errorf("own addresses read %d times from the usage and %d from the host, want once each", n, hostReads)
	}
	ageOwnAddresses(h)
	_ = check("tcp4", "192.168.1.30:9393", nil)
	if n := usage.reads.Load(); n != 2 || hostReads != 2 {
		t.Errorf("own addresses read %d times from the usage and %d from the host, want twice each once old", n, hostReads)
	}

	// When the usage cannot be read, the network cards read last are still
	// refused, and the next check reads the usage again, but not the host's
	// addresses, which are still new.
	ageOwnAddresses(h)
	usage.failing.Store(true)
	if err := check("tcp4", "192.168.1.20:9393", nil); !errors.Is(err, errOwnAddress) {
		t.Errorf("connecting to 192.168.1.20:9393 while the usage cannot be read error = %v, want errOwnAddress", err)
	}
	usage.failing.Store(false)
	if err := check("tcp4", "192.168.1.20:9393", nil); !errors.Is(err, errOwnAddress) {
		t.Errorf("connecting to 192.168.1.20:9393 after a failed reading error = %v, want errOwnAddress", err)
	}
	if n := usage.reads.Load(); n != 3 {
		t.Errorf("usage read %d times, want not again right after the failed reading", n)
	}
	h.ownMu.Lock()
	h.cardsFailed = h.cardsFailed.Add(-ownRetryAfter)
	h.ownMu.Unlock()
	if err := check("tcp4", "192.168.1.20:9393", nil); !errors.Is(err, errOwnAddress) {
		t.Errorf("connecting to 192.168.1.20:9393 after a failed reading error = %v, want errOwnAddress", err)
	}
	if n := usage.reads.Load(); n != 4 || hostReads != 3 {
		t.Errorf("own addresses read %d times from the usage and %d from the host, want the usage once more a second after the failed reading and the host not", n, hostReads)
	}
}

// ageOwnAddresses makes the hub's own addresses old enough to be read again.
func ageOwnAddresses(h *Hub) {
	h.ownMu.Lock()
	defer h.ownMu.Unlock()
	h.hostRead = h.hostRead.Add(-ownAddressesFor)
	if !h.cardsRead.IsZero() {
		h.cardsRead = h.cardsRead.Add(-ownAddressesFor)
	}
}

// blockingUsage is the hub's own usage, with a network card at
// 192.168.1.20. Each reading is sent on reading and then waits for release
// to be closed. When failing is set, every reading fails.
type blockingUsage struct {
	reading chan struct{}
	release chan struct{}
	failing bool
}

func (u *blockingUsage) Collect(context.Context) (metrics.Snapshot, error) {
	u.reading <- struct{}{}
	<-u.release
	if u.failing {
		return metrics.Snapshot{}, errors.New("cannot read the usage")
	}
	return metrics.Snapshot{Network: []metrics.NetworkInterface{{Name: "eth0", Addresses: []string{"192.168.1.20"}}}}, nil
}

func TestOwnAddressesAreReadByOneCallerAtATime(t *testing.T) {
	h := openTestHub(t, openTestStore(t), nil)
	h.hostAddrs = func() []netip.Addr { return nil }
	usage := &blockingUsage{reading: make(chan struct{}), release: make(chan struct{})}
	h.local = usage
	want := []netip.Addr{netip.MustParseAddr("192.168.1.20")}
	ask := func() chan []netip.Addr {
		got := make(chan []netip.Addr, 1)
		go func() { got <- h.ownAddresses() }()
		return got
	}

	// Without a list yet, a second caller waits for the first one's reading,
	// and does not read the usage itself.
	first := ask()
	<-usage.reading
	second := ask()
	select {
	case got := <-second:
		t.Fatalf("ownAddresses() = %v while the first reading is not done, want it to wait for it", got)
	case <-time.After(50 * time.Millisecond):
	}
	close(usage.release)
	for _, got := range []chan []netip.Addr{first, second} {
		if own := <-got; !slices.Equal(own, want) {
			t.Errorf("ownAddresses() = %v, want %v", own, want)
		}
	}

	// Once the list is old, one caller reads it again, and the others go on
	// with the list read last meanwhile.
	usage.release = make(chan struct{})
	t.Cleanup(func() { close(usage.release) })
	ageOwnAddresses(h)
	ask()
	<-usage.reading
	select {
	case own := <-ask():
		if !slices.Equal(own, want) {
			t.Errorf("ownAddresses() = %v while the usage is read again, want %v read last", own, want)
		}
	case <-time.After(time.Second):
		t.Fatal("ownAddresses() waits while the usage is read again, want the list read last at once")
	}
}

func TestOwnAddressesGoOnWithTheHostsWhileTheUsageCannotBeRead(t *testing.T) {
	h := openTestHub(t, openTestStore(t), nil)
	host := []netip.Addr{netip.MustParseAddr("172.18.0.1")}
	h.hostAddrs = func() []netip.Addr { return host }
	usage := &blockingUsage{reading: make(chan struct{}), release: make(chan struct{}), failing: true}
	h.local = usage
	ask := func() chan []netip.Addr {
		got := make(chan []netip.Addr, 1)
		go func() { got <- h.ownAddresses() }()
		return got
	}
	atOnce := func(got chan []netip.Addr, when string) {
		t.Helper()
		select {
		case own := <-got:
			if !slices.Equal(own, host) {
				t.Errorf("ownAddresses() = %v %s, want the host's %v", own, when, host)
			}
		case <-time.After(time.Second):
			t.Fatalf("ownAddresses() waits for the usage %s, want the host's addresses at once", when)
		}
	}

	// The first reading is waited for...
	first := ask()
	<-usage.reading
	second := ask()
	select {
	case got := <-second:
		t.Fatalf("ownAddresses() = %v while the first reading is not done, want it to wait for it", got)
	case <-time.After(50 * time.Millisecond):
	}
	close(usage.release)
	atOnce(first, "after the first reading failed")
	atOnce(second, "after the first reading failed")
	// ...but once it failed, the usage is not read again for a second...
	usage.release = make(chan struct{})
	atOnce(ask(), "right after the first reading failed")
	// ...and then by one caller, while the others go on without it.
	h.ownMu.Lock()
	h.cardsFailed = h.cardsFailed.Add(-ownRetryAfter)
	h.ownMu.Unlock()
	retry := ask()
	<-usage.reading
	atOnce(ask(), "while it is read again")
	close(usage.release)
	atOnce(retry, "after reading it again failed")
}

// countingDevice is a usage-control that counts the connections opened to
// it and tells the connection, by its address, that the newest reading came
// over. A request for its minutes is sent on held and waits until the
// channel it comes with is closed.
type countingDevice struct {
	address string
	opened  atomic.Int32
	last    atomic.Value
	held    chan heldRequest
}

// heldRequest is a request for a countingDevice's minutes, over the
// connection from remote, which waits until release is closed.
type heldRequest struct {
	remote  string
	release chan struct{}
}

func startCountingDevice(t *testing.T) *countingDevice {
	t.Helper()
	d := &countingDevice{held: make(chan heldRequest)}
	device := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == MinutesPath {
			release := make(chan struct{})
			d.held <- heldRequest{remote: r.RemoteAddr, release: release}
			<-release
			_, _ = w.Write([]byte(`{}`))
			return
		}
		d.last.Store(r.RemoteAddr)
		_, _ = w.Write([]byte(`{"cpu":{"usagePercent":12.5,"cores":4}}`))
	}))
	device.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			d.opened.Add(1)
		}
	}
	device.Start()
	t.Cleanup(device.Close)
	d.address = strings.TrimPrefix(device.URL, "http://")
	return d
}

// openOwnAddressesHub opens a hub whose usage lists a network card at
// 192.168.1.20.
func openOwnAddressesHub(t *testing.T) (*Hub, *countingUsage) {
	t.Helper()
	h := openTestHub(t, openTestStore(t), nil)
	h.hostAddrs = func() []netip.Addr { return nil }
	usage := &countingUsage{addresses: []string{"192.168.1.20"}}
	h.local = usage
	return h, usage
}

// gatedUsage is the hub's own usage, with a network card at 192.168.1.20.
// Each reading is sent on reading and then waits for a send on release,
// until done is closed.
type gatedUsage struct {
	reading chan struct{}
	release chan struct{}
	done    chan struct{}
}

func (u *gatedUsage) Collect(context.Context) (metrics.Snapshot, error) {
	select {
	case u.reading <- struct{}{}:
	case <-u.done:
		return metrics.Snapshot{}, errors.New("the test is over")
	}
	select {
	case <-u.release:
	case <-u.done:
	}
	return metrics.Snapshot{Network: []metrics.NetworkInterface{{Name: "eth0", Addresses: []string{"192.168.1.20"}}}}, nil
}

func TestAddDoesNotReadTheOwnAddressesHoldingMu(t *testing.T) {
	h := openTestHub(t, openTestStore(t), nil)
	h.hostAddrs = func() []netip.Addr { return nil }
	usage := &gatedUsage{reading: make(chan struct{}), release: make(chan struct{}), done: make(chan struct{})}
	t.Cleanup(func() { close(usage.done) })
	h.local = usage
	// The device answers only once the own addresses read before asking it
	// are old enough to be read again, as after a slow answer.
	var aged sync.Once
	device := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		aged.Do(func() { ageOwnAddresses(h) })
		_, _ = w.Write([]byte(`{"cpu":{"usagePercent":12.5,"cores":4}}`))
	}))
	t.Cleanup(device.Close)

	added := make(chan error, 1)
	go func() {
		_, err := h.Add(context.Background(), "Laptop", strings.TrimPrefix(device.URL, "http://"), KindServer)
		added <- err
	}()
	// Adding reads them before asking the device.
	<-usage.reading
	usage.release <- struct{}{}
	// They are read again only by the new device's recorder, which does
	// not hold mu, so Add finishes meanwhile.
	<-usage.reading
	select {
	case err := <-added:
		if err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Add() waits for the hub's usage to be read, want it to use the own addresses read before asking the device")
	}
	if !h.mu.TryLock() {
		t.Fatal("mu is held while the hub's usage is read")
	}
	h.mu.Unlock()
	usage.release <- struct{}{}
}

func TestCollectingFromADeviceAddedOnThePageNoticesNewOwnAddresses(t *testing.T) {
	ctx := context.Background()
	h, usage := openOwnAddressesHub(t)
	added, fixed := startCountingDevice(t), startCountingDevice(t)
	addedAgent := h.pageAgent(added.address)
	fixedAgent := newAgent(fixed.address, func(string, string, syscall.RawConn) error { return nil })
	collect := func() {
		t.Helper()
		for _, agent := range []*Agent{addedAgent, fixedAgent} {
			if _, err := agent.Collect(ctx); err != nil {
				t.Fatalf("Collect() error = %v", err)
			}
		}
	}

	collect()
	collect()
	if added.opened.Load() != 1 || fixed.opened.Load() != 1 {
		t.Fatalf("connections opened = %d and %d, want one each, kept open between readings", added.opened.Load(), fixed.opened.Load())
	}

	// The hub gains an address while the device answers every reading: the
	// next reading of the device added on the page reads the hub's own
	// addresses and connects anew, so it is checked; the one from
	// HUB_DEVICES keeps its connection.
	usage.setAddresses("192.168.1.20", "192.168.1.21")
	ageOwnAddresses(h)
	collect()
	if n := usage.reads.Load(); n != 2 {
		t.Errorf("usage read %d times, want once more once the hub's addresses are old", n)
	}
	if added.opened.Load() != 2 || fixed.opened.Load() != 1 {
		t.Errorf("connections opened = %d and %d, want a new one to the device added on the page only", added.opened.Load(), fixed.opened.Load())
	}
	collect()
	if added.opened.Load() != 2 {
		t.Errorf("connections opened = %d, want the new one kept open", added.opened.Load())
	}
}

func TestAConnectionInUseWhenTheOwnAddressesChangeIsNotUsedAgain(t *testing.T) {
	ctx := context.Background()
	h, usage := openOwnAddressesHub(t)
	device := startCountingDevice(t)
	agent := h.pageAgent(device.address)
	if _, err := agent.Collect(ctx); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	fetch := func() chan error {
		done := make(chan error, 1)
		go func() {
			var answer any
			done <- agent.get(ctx, agent.minutesURL, 1<<10, &answer)
		}()
		return done
	}

	// The minutes are fetched over the connection kept open...
	firstDone := fetch()
	first := <-device.held
	// ...when the hub gains an address, and the next request connects anew.
	usage.setAddresses("192.168.1.20", "192.168.1.21")
	ageOwnAddresses(h)
	secondDone := fetch()
	second := <-device.held
	if second.remote == first.remote {
		t.Fatal("the second request went over the first one's connection, want a new one")
	}
	// The first connection is free again first, the second one after it.
	close(first.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("get() error = %v", err)
	}
	close(second.release)
	if err := <-secondDone; err != nil {
		t.Fatalf("get() error = %v", err)
	}

	if _, err := agent.Collect(ctx); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if last, _ := device.last.Load().(string); last == first.remote {
		t.Errorf("the reading went over %s, the connection opened before the hub gained an address, want another", last)
	}
}

func TestSuggestLeavesOutTheHubsOwnAddresses(t *testing.T) {
	h, _ := openOwnAddressesHub(t)
	h.suggester = testSuggester("Office PC")
	if got, ok := h.Suggest(context.Background(), netip.MustParseAddr("192.168.1.20")); ok {
		t.Errorf("Suggest(192.168.1.20) = %+v, want no suggestion for the address of the hub's network card", got)
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
	h, err := newHub(running, store, []Device{{ID: "vm", Name: "VM", Address: fixed}}, history.DefaultMaxEntries, 30*24*time.Hour, "9393", nil, false)
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
	if _, err := h.Add(ctx, "Office PC", startDevice(t), KindServer); err != nil {
		t.Errorf("Add(Office PC) after deleting error = %v", err)
	}
}

func TestNoChangesOnceTheHubStops(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	running, stop := context.WithCancel(ctx)
	h, err := newHub(running, store, nil, history.DefaultMaxEntries, 30*24*time.Hour, "9393", nil, true)
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
	h, err := newHub(first, store, []Device{pi, nas}, 0, 30*24*time.Hour, "9393", nil, true)
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

	// Added again a day after it was removed, the device continues them, and
	// the day it was removed does not count as watched.
	var sinceBefore int64
	if err := store.DB().QueryRow(`SELECT since FROM hub_watched WHERE device = 'laptop'`).Scan(&sinceBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`UPDATE hub_kept SET removed = removed - 86400`); err != nil {
		t.Fatal(err)
	}
	if _, err := again.Add(ctx, "Laptop", startDevice(t), KindPC); err != nil {
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
	if _, err := h.Add(ctx, "Laptop", startDevice(t), KindPC); err != nil {
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
		if _, err := h.Add(ctx, name, startDevice(t), KindPC); err != nil {
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
