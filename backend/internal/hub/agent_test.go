package hub

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

func TestAgentReadsTheDevicesUsage(t *testing.T) {
	device := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/metrics" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"time":"2001-01-01T00:00:00Z","cpu":{"usagePercent":12.5,"cores":4}}`))
	}))
	defer device.Close()
	agent := NewAgent(strings.TrimPrefix(device.URL, "http://"))

	if _, err := agent.Latest().Collect(context.Background()); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("Latest before the first reading: error = %v, want ErrUnreachable", err)
	}

	got, err := agent.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	want := metrics.CPU{UsagePercent: 12.5, Cores: 4}
	if !reflect.DeepEqual(got.CPU, want) {
		t.Errorf("CPU = %+v, want %+v", got.CPU, want)
	}
	if time.Since(got.Time) > time.Minute {
		t.Errorf("Time = %v, want the time the answer arrived, not the device's clock", got.Time)
	}

	latest, err := agent.Latest().Collect(context.Background())
	if err != nil || !reflect.DeepEqual(latest.CPU, want) {
		t.Errorf("Latest = %+v, %v; want the reading Collect read", latest.CPU, err)
	}
}

func TestAgentSaysWhenAnAnswerIsTooLarge(t *testing.T) {
	device := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"disks":[{"path":"` + strings.Repeat("x", maxResponseBytes) + `"}]}`))
	}))
	defer device.Close()
	agent := NewAgent(strings.TrimPrefix(device.URL, "http://"))

	if _, err := agent.Collect(context.Background()); err == nil || !strings.Contains(err.Error(), "larger than the 1024 KiB") {
		t.Errorf("Collect() error = %v, want it to say the answer is too large", err)
	}
}

func TestAgentReportsAFailingDevice(t *testing.T) {
	device := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "only reachable from the local network", http.StatusForbidden)
	}))
	defer device.Close()
	agent := NewAgent(strings.TrimPrefix(device.URL, "http://"))

	if _, err := agent.Collect(context.Background()); err == nil {
		t.Error("Collect() error = nil, want the device's status")
	}
}

func TestAgentOnlyConnectsToTheLocalNetwork(t *testing.T) {
	agent := NewAgent("203.0.113.5:9393")

	_, err := agent.Collect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not on the local network") {
		t.Errorf("Collect() error = %v, want it refused as not on the local network", err)
	}
}

func TestAgentTellsTheHubsPagePortAndID(t *testing.T) {
	sent := make(chan http.Header, 1)
	device := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent <- r.Header
		_, _ = w.Write([]byte(`{}`))
	}))
	defer device.Close()
	agent := NewAgent(strings.TrimPrefix(device.URL, "http://"))
	agent.pagePort = "9393"
	agent.hubID = "0123456789abcdef0123456789abcdef"

	if _, err := agent.Collect(context.Background()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	got := <-sent
	if got.Get(PagePortHeader) != "9393" || got.Get(HubIDHeader) != agent.hubID {
		t.Errorf("%s = %q, %s = %q; want 9393 and %s", PagePortHeader, got.Get(PagePortHeader), HubIDHeader, got.Get(HubIDHeader), agent.hubID)
	}
}

func TestAgentCleansTheExtrasOfTheAnswer(t *testing.T) {
	device := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"time":"2001-01-01T00:00:00Z","extras":[
			{"id":"pressure","title":"Pressure","items":[{"id":"cpu","label":"CPU","unit":"percent","value":2,"history":true}]},
			{"id":"Not valid","title":"x","items":[{"id":"a","label":"A","unit":"number","value":1}]}
		]}`))
	}))
	defer device.Close()
	agent := NewAgent(strings.TrimPrefix(device.URL, "http://"))

	got, err := agent.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(got.Extras) != 1 || got.Extras[0].ID != "pressure" || *got.Extras[0].Items[0].Value != 2 {
		t.Errorf("Extras = %+v, want only the valid group", got.Extras)
	}
}

// closeCountingTransport counts how often its idle connections are closed.
type closeCountingTransport struct {
	*http.Transport
	closes atomic.Int32
}

func (t *closeCountingTransport) CloseIdleConnections() {
	t.closes.Add(1)
	t.Transport.CloseIdleConnections()
}

func TestAgentClosesItsConnectionsOnceWhenTheOwnAddressesGainOneDuringARequest(t *testing.T) {
	var generation atomic.Uint64
	var bump atomic.Bool
	var opened atomic.Int32
	device := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if bump.CompareAndSwap(true, false) {
			generation.Add(1)
		}
		_, _ = w.Write([]byte(`{"cpu":{"usagePercent":12.5,"cores":4}}`))
	}))
	device.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			opened.Add(1)
		}
	}
	device.Start()
	t.Cleanup(device.Close)
	agent := newAgent(strings.TrimPrefix(device.URL, "http://"), func(string, string, syscall.RawConn) error { return nil })
	agent.checkOwnGeneration(generation.Load)
	transport := &closeCountingTransport{Transport: agent.client.Transport.(*http.Transport)}
	agent.client.Transport = transport
	collect := func() {
		t.Helper()
		if _, err := agent.Collect(context.Background()); err != nil {
			t.Fatalf("Collect() error = %v", err)
		}
	}

	collect()
	// The hub's own addresses gain one while the device answers: the
	// connection is closed before the next request, and not once more
	// before the one after it, whose new connection is kept.
	bump.Store(true)
	collect()
	collect()
	collect()
	if n := transport.closes.Load(); n != 1 {
		t.Errorf("idle connections closed %d times, want once, before the request after the one the addresses gained one during", n)
	}
	if n := opened.Load(); n != 2 {
		t.Errorf("connections opened = %d, want 2: one more after the addresses gained one, kept open then", n)
	}
}

func TestAgentDoesNotReuseAConnectionOpenedBeforeTheOwnAddressesGainedOne(t *testing.T) {
	var generation atomic.Uint64
	var opened atomic.Int32
	var last atomic.Value
	device := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last.Store(r.RemoteAddr)
		_, _ = w.Write([]byte(`{"cpu":{"usagePercent":12.5,"cores":4}}`))
	}))
	device.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			opened.Add(1)
		}
	}
	device.Start()
	t.Cleanup(device.Close)
	agent := newAgent(strings.TrimPrefix(device.URL, "http://"), func(string, string, syscall.RawConn) error { return nil })
	agent.checkOwnGeneration(generation.Load)
	collect := func() string {
		t.Helper()
		if _, err := agent.Collect(context.Background()); err != nil {
			t.Fatalf("Collect() error = %v", err)
		}
		remote, _ := last.Load().(string)
		return remote
	}

	first := collect()
	// The hub's own addresses gained one while the connection was in use by
	// another request, after a third one closed the idle connections for
	// it: the connection is kept again, but not used.
	generation.Add(1)
	agent.mu.Lock()
	agent.seenGeneration = generation.Load()
	agent.mu.Unlock()
	if second := collect(); second == first {
		t.Fatalf("the request went over %s, opened before the hub gained an address, want a new connection", second)
	}
	collect()
	if n := opened.Load(); n != 2 {
		t.Errorf("connections opened = %d, want 2: one more after the addresses gained one, kept open then", n)
	}
}

func TestAgentReusesItsConnectionForALargeAnswer(t *testing.T) {
	var opened atomic.Int32
	// Some 100 KB, more than the device's write buffer, so the answer is
	// chunked, and ending in a newline, as the device's API writes it. The
	// newline and the end of the answer come a moment after the JSON, so
	// the decoder never reads them along with it.
	answer := `{"disks":[{"path":"` + strings.Repeat("x", 100<<10) + `"}],"cpu":{"usagePercent":12.5,"cores":4}}`
	device := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(answer))
		w.(http.Flusher).Flush()
		time.Sleep(20 * time.Millisecond)
		_, _ = w.Write([]byte("\n"))
	}))
	device.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			opened.Add(1)
		}
	}
	device.Start()
	t.Cleanup(device.Close)
	agent := NewAgent(strings.TrimPrefix(device.URL, "http://"))

	for range 4 {
		if _, err := agent.Collect(context.Background()); err != nil {
			t.Fatalf("Collect() error = %v", err)
		}
	}
	if n := opened.Load(); n != 1 {
		t.Errorf("connections opened = %d, want 1, used again for each answer", n)
	}
}

func TestAgentUsesANewConnectionOpenedWhileTheOwnAddressesGainOne(t *testing.T) {
	var generation atomic.Uint64
	device := startDevice(t)
	// The own addresses gain one while the agent connects, after it read
	// their generation: the new connection is behind, but was checked when
	// opened, and closing it would fail the reading instead of retrying.
	agent := newAgent(device, func(string, string, syscall.RawConn) error {
		generation.Add(1)
		return nil
	})
	agent.checkOwnGeneration(generation.Load)

	if _, err := agent.Collect(context.Background()); err != nil {
		t.Errorf("Collect() error = %v, want the reading over the new connection", err)
	}
}
