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
		askName: func(_ context.Context, address string) (string, error) {
			if address != "192.168.1.20:9393" || agentName == "" {
				return "", errors.New("connection refused")
			}
			return agentName, nil
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
		{"a usage-control that names itself", "192.168.1.20", "Christof's PC", nil, &Suggestion{"192.168.1.20:9393", "Christof's PC"}},
		{"named by the DNS", "192.168.1.20", "", nil, &Suggestion{"192.168.1.20:9393", "office-pc"}},
		{"no name known", "192.168.1.30", "", nil, &Suggestion{"192.168.1.30:9393", ""}},
		{"an IPv4-mapped address", "::ffff:192.168.1.30", "", nil, &Suggestion{"192.168.1.30:9393", ""}},
		{"IPv6", "fd00::30", "", nil, &Suggestion{"[fd00::30]:9393", ""}},
		{"a name that cannot be added", "192.168.1.20", "local", nil, &Suggestion{"192.168.1.20:9393", "office-pc"}},
		{"already added by address", "192.168.1.20", "", []Device{{ID: "pc", Name: "PC", Address: "192.168.1.20:9393"}}, nil},
		{"already added by name", "192.168.1.20", "", []Device{{ID: "pc", Name: "PC", Address: "office-pc.fritz.box:9393"}}, nil},
		{"already added under its own name", "192.168.1.20", "Office PC", []Device{{ID: "office-pc", Name: "Office PC", Address: "pc.lan:9393"}}, nil},
		{"added on another port", "192.168.1.20", "", []Device{{ID: "pc", Name: "PC", Address: "192.168.1.20:8080"}}, &Suggestion{"192.168.1.20:9393", "office-pc"}},
		{"another device is added", "192.168.1.30", "", []Device{{ID: "pc", Name: "PC", Address: "office-pc.fritz.box:9393"}}, &Suggestion{"192.168.1.30:9393", ""}},
		{"the hub itself", "192.168.1.9", "", nil, nil},
		{"loopback", "127.0.0.1", "", nil, nil},
		{"the Docker gateway", "172.17.0.1", "", nil, nil},
		{"link-local IPv6", "fe80::1", "", nil, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := testSuggester(test.agentName).suggest(context.Background(), netip.MustParseAddr(test.from), test.known)
			switch {
			case test.want == nil && ok:
				t.Errorf("suggest(%s) = %+v, want no suggestion", test.from, got)
			case test.want != nil && (!ok || got != *test.want):
				t.Errorf("suggest(%s) = %+v, %v; want %+v", test.from, got, ok, *test.want)
			}
		})
	}
}

func TestSuggestAsksTheDevice(t *testing.T) {
	device := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"name":"Office PC","cpu":{"usagePercent":12.5,"cores":4}}`))
	}))
	t.Cleanup(device.Close)
	address := strings.TrimPrefix(device.URL, "http://")
	s := testSuggester("")
	s.askName = defaultSuggester().askName

	name, err := s.askName(context.Background(), address)
	if err != nil || name != "Office PC" {
		t.Errorf("askName() = %q, %v; want the name the device reports", name, err)
	}
}
