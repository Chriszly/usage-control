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
	h, err := New(ctx, store, fixed)
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

	device, err := h.Add(ctx, "Office PC", address)
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
		{"Laptop", "192.168.1.30", ProblemAddress},
		{"--", address, ProblemName},
		{"Laptop", "127.0.0.1:1", ProblemUnreachable},
		{"Laptop", "203.0.113.5:8080", ProblemUnreachable},
	}
	for _, tt := range tests {
		if _, err := h.Add(ctx, tt.name, tt.address); problemOf(err) != tt.want {
			t.Errorf("Add(%q, %q) error = %v, want problem %q", tt.name, tt.address, err, tt.want)
		}
	}
}

func TestRemoveForgetsTheDeviceAndItsHistory(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	address := startDevice(t)
	h := openTestHub(t, store, []Device{{ID: "pi", Name: "Pi", Address: address}})
	for _, name := range []string{"Office PC", "Laptop"} {
		if _, err := h.Add(ctx, name, address); err != nil {
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
