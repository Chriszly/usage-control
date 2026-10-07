package hub

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
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

func TestAgentTellsTheHubsPagePort(t *testing.T) {
	sent := make(chan string, 1)
	device := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent <- r.Header.Get(PagePortHeader)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer device.Close()
	agent := NewAgent(strings.TrimPrefix(device.URL, "http://"))
	agent.pagePort = "9393"

	if _, err := agent.Collect(context.Background()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if got := <-sent; got != "9393" {
		t.Errorf("%s = %q, want 9393", PagePortHeader, got)
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
