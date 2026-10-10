package hub

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// fakeRouter reads a router that answers while up is set.
type fakeRouter struct {
	up    atomic.Bool
	reads atomic.Int32
}

func (f *fakeRouter) Collect(context.Context) (metrics.Snapshot, error) {
	f.reads.Add(1)
	if !f.up.Load() {
		return metrics.Snapshot{}, errors.New("the router does not answer")
	}
	return metrics.Snapshot{Network: []metrics.NetworkInterface{{Name: "WAN", ReceiveBytesPerSecond: 1000}}}, nil
}

func TestARouterIsADeviceWithAvailability(t *testing.T) {
	ctx := context.Background()
	source := &fakeRouter{}
	source.up.Store(true)
	router, err := NewRouter(" Home router ", "192.168.1.1", source)
	if err != nil {
		t.Fatal(err)
	}
	if router.ID != "home-router" || router.Name != "Home router" {
		t.Fatalf("got %+v", router)
	}
	h := openTestHub(t, openTestStore(t), []Device{router})

	remotes := h.Remotes()
	if len(remotes) != 1 || !remotes[0].Router() || !remotes[0].Fixed {
		t.Fatalf("got %+v", remotes)
	}
	remote := remotes[0]
	deadline := time.Now().Add(5 * time.Second)
	for !remote.Agent.answers() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	snapshot, err := remote.Agent.Latest().Collect(ctx)
	if err != nil {
		t.Fatalf("the router's reading: %v", err)
	}
	if len(snapshot.Network) != 1 || snapshot.Network[0].Name != "WAN" {
		t.Errorf("got %+v", snapshot)
	}
	availability, err := remote.Availability(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if availability.Kind != KindServer {
		t.Errorf("a router's kind is %q, want a server's", availability.Kind)
	}
	if err := h.Remove(ctx, router.ID, false); problemOf(err) != ProblemFixed {
		t.Errorf("Remove() = %v, want a router from HUB_ROUTERS to stay", err)
	}

	// A router that stops answering is unreachable, as any device.
	source.up.Store(false)
	if _, err := remote.watched.Collect(ctx); err == nil {
		t.Error("a router that does not answer was read")
	}
}

func TestCheckFixedRefusesTheSameNameTwice(t *testing.T) {
	device, err := NewDevice("Router", "192.168.1.20:9393")
	if err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter("router", "192.168.1.1", &fakeRouter{})
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckFixed([]Device{device, router}); err == nil {
		t.Error("a device and a router with the same name were taken")
	}
	if _, err := NewRouter("Local", "192.168.1.1", &fakeRouter{}); err == nil {
		t.Error("a router with the name of the hub itself was taken")
	}
}
