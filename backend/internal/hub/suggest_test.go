package hub

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// testSuggester answers like a home network: the hub is 192.168.1.9 in a
// Docker network with the gateway 172.17.0.1, office-pc.fritz.box is
// 192.168.1.20, and the device at 192.168.1.20 calls itself agentName.
func testSuggester(agentName string) suggester {
	return suggester{
		port: DefaultPort,
		ownAddrs: func() ([]net.Addr, error) {
			return []net.Addr{&net.IPNet{IP: net.ParseIP("192.168.1.9"), Mask: net.CIDRMask(24, 32)}}, nil
		},
		gateways: func() []netip.Addr { return []netip.Addr{netip.MustParseAddr("172.17.0.1")} },
		ask: func(_ context.Context, address string) (metrics.Snapshot, error) {
			if address != "192.168.1.20:9393" || agentName == "" {
				return metrics.Snapshot{}, errors.New("connection refused")
			}
			return metrics.Snapshot{Name: agentName, CPU: metrics.CPU{LoadAverage: &metrics.LoadAverage{}}}, nil
		},
		lookupAddr: func(_ context.Context, addr string) ([]string, error) {
			if addr == "192.168.1.20" {
				return []string{"office-pc.fritz.box."}, nil
			}
			return nil, errors.New("no such host")
		},
		lookupHost: func(_ context.Context, host string) ([]string, error) {
			if host == "office-pc.fritz.box" {
				return []string{"192.168.1.20"}, nil
			}
			return nil, errors.New("no such host")
		},
	}
}

func TestSuggest(t *testing.T) {
	tests := []struct {
		name      string
		from      string
		agentName string
		known     []Device
		want      *Suggestion
	}{
		{"a usage-control that names itself", "192.168.1.20", "Christof's PC", nil, &Suggestion{"192.168.1.20:9393", "Christof's PC", KindServer}},
		{"named by the DNS", "192.168.1.20", "", nil, &Suggestion{"192.168.1.20:9393", "office-pc", KindServer}},
		{"no name known", "192.168.1.30", "", nil, &Suggestion{"192.168.1.30:9393", "", KindServer}},
		{"an IPv4-mapped address", "::ffff:192.168.1.30", "", nil, &Suggestion{"192.168.1.30:9393", "", KindServer}},
		{"IPv6", "fd00::30", "", nil, &Suggestion{"[fd00::30]:9393", "", KindServer}},
		{"a name that cannot be added", "192.168.1.20", "local", nil, &Suggestion{"192.168.1.20:9393", "office-pc", KindServer}},
		{"already added by address", "192.168.1.20", "", []Device{{ID: "pc", Name: "PC", Address: "192.168.1.20:9393"}}, nil},
		{"already added by name", "192.168.1.20", "", []Device{{ID: "pc", Name: "PC", Address: "office-pc.fritz.box:9393"}}, nil},
		{"already added under its own name", "192.168.1.20", "Office PC", []Device{{ID: "office-pc", Name: "Office PC", Address: "pc.lan:9393"}}, nil},
		{"another device with the same name", "192.168.1.20", "raspberrypi", []Device{{ID: "raspberrypi", Name: "raspberrypi", Address: "192.168.1.21:9393"}}, &Suggestion{"192.168.1.20:9393", "office-pc", KindServer}},
		{"another device has the DNS name", "192.168.1.20", "", []Device{{ID: "office-pc", Name: "office-pc", Address: "192.168.1.21:9393"}}, &Suggestion{"192.168.1.20:9393", "", KindServer}},
		{"added on another port", "192.168.1.20", "", []Device{{ID: "pc", Name: "PC", Address: "192.168.1.20:8080"}}, &Suggestion{"192.168.1.20:9393", "office-pc", KindServer}},
		{"another device is added", "192.168.1.30", "", []Device{{ID: "pc", Name: "PC", Address: "office-pc.fritz.box:9393"}}, &Suggestion{"192.168.1.30:9393", "", KindServer}},
		{"the hub itself", "192.168.1.9", "", nil, nil},
		{"loopback", "127.0.0.1", "", nil, nil},
		{"the Docker gateway", "172.17.0.1", "", nil, nil},
		{"link-local IPv6", "fe80::1", "", nil, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := testSuggester(test.agentName).suggest(context.Background(), netip.MustParseAddr(test.from), test.known, nil)
			switch {
			case test.want == nil && ok:
				t.Errorf("suggest(%s) = %+v, want no suggestion", test.from, got)
			case test.want != nil && (!ok || got != *test.want):
				t.Errorf("suggest(%s) = %+v, %v; want %+v", test.from, got, ok, *test.want)
			}
		})
	}
}

func TestSuggestTakesALaptopForAPC(t *testing.T) {
	s := testSuggester("")
	s.ask = func(context.Context, string) (metrics.Snapshot, error) {
		return metrics.Snapshot{Name: "Laptop", CPU: metrics.CPU{LoadAverage: &metrics.LoadAverage{}}, Battery: &metrics.Battery{}}, nil
	}
	got, ok := s.suggest(context.Background(), netip.MustParseAddr("192.168.1.20"), nil, nil)
	if want := (Suggestion{"192.168.1.20:9393", "Laptop", KindPC}); !ok || got != want {
		t.Errorf("suggest() = %+v, %v; want %+v", got, ok, want)
	}
}

func TestKindOf(t *testing.T) {
	tests := []struct {
		name     string
		snapshot metrics.Snapshot
		want     Kind
	}{
		{"Linux without a battery", metrics.Snapshot{CPU: metrics.CPU{LoadAverage: &metrics.LoadAverage{}}}, KindServer},
		{"Linux with a battery", metrics.Snapshot{CPU: metrics.CPU{LoadAverage: &metrics.LoadAverage{}}, Battery: &metrics.Battery{}}, KindPC},
		{"Windows", metrics.Snapshot{}, KindPC},
	}
	for _, test := range tests {
		if got := kindOf(test.snapshot); got != test.want {
			t.Errorf("%s: kindOf() = %q, want %q", test.name, got, test.want)
		}
	}
}

func TestSuggestAsksTheDevice(t *testing.T) {
	device := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"name":"Office PC","cpu":{"usagePercent":12.5,"cores":4}}`))
	}))
	t.Cleanup(device.Close)
	address := strings.TrimPrefix(device.URL, "http://")

	snapshot, err := defaultSuggester().ask(context.Background(), address)
	if err != nil || snapshot.Name != "Office PC" {
		t.Errorf("ask() = %+v, %v; want the name the device reports", snapshot, err)
	}
}

func TestSuggestWithoutOwnAddrsAsksTheSystem(t *testing.T) {
	s := testSuggester("")
	s.ownAddrs = nil
	if _, ok := s.suggest(context.Background(), netip.MustParseAddr("127.0.0.1"), nil, nil); ok {
		t.Error("suggest(127.0.0.1) offered the hub itself")
	}
	if !s.isOwn(netip.MustParseAddr("127.0.0.1"), nil) {
		t.Error("isOwn(127.0.0.1) = false without ownAddrs, want the system's addresses")
	}
}

func TestSuggestLeavesOutTheHostsAddresses(t *testing.T) {
	// In Docker, the host's address is not one of the container's interfaces.
	own := []netip.Addr{netip.MustParseAddr("192.168.1.20")}
	if got, ok := testSuggester("Pi").suggest(context.Background(), netip.MustParseAddr("192.168.1.20"), nil, own); ok {
		t.Errorf("suggest() = %+v, want no suggestion for the host's own address", got)
	}
}
