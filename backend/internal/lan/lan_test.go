package lan

import (
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"
)

// ownAddresses lists the addresses of a machine on a dual-stack home network.
func ownAddresses() ([]net.Addr, error) {
	var addrs []net.Addr
	for _, cidr := range []string{"127.0.0.1/8", "::1/128", "192.168.1.9/24", "fe80::1/64", "2001:db8:1:2::9/64", "fd12::9/64"} {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, err
		}
		addrs = append(addrs, ipNet)
	}
	return addrs, nil
}

func TestLocal(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"192.168.1.20", true},
		{"10.1.2.3", true},
		{"172.16.0.9", true},
		{"169.254.10.10", true},
		{"fd00::1", true},
		{"fe80::1", true},
		{"::ffff:192.168.1.20", true},
		{"2001:db8:1:2::20", true},       // the subnet of one of the machine's own addresses
		{"::ffff:8.8.8.8", false},        // a public IPv4 address, mapped
		{"8.8.8.8", false},               // a public IPv4 address
		{"172.32.0.1", false},            // just outside 172.16/12
		{"2001:db8:1:3::20", false},      // another /64 of the same provider
		{"2001:db8:9::1", false},         // another global IPv6 address
		{"2001:db8:1:2:ffff::", true},    // still inside the own /64
		{"ff02::1", false},               // multicast
		{"2001:db8:1:2::20%eth0", false}, // a zone is not accepted by the parser
		{"fd12:0:0:0::1", true},          // unique local, own or not
	}
	checker := &Checker{Interfaces: ownAddresses}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			addr, err := netip.ParseAddr(tt.addr)
			if err != nil {
				if tt.want {
					t.Fatalf("ParseAddr(%q) error = %v", tt.addr, err)
				}
				return
			}
			if got := checker.Local(addr); got != tt.want {
				t.Errorf("Local(%s) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

func TestLocalAddrPort(t *testing.T) {
	checker := &Checker{Interfaces: ownAddresses}
	tests := []struct {
		address string
		want    bool
	}{
		{"192.168.1.20:5000", true},
		{"[2001:db8:1:2::20]:5000", true},
		{"[2001:db8:9::1]:5000", false},
		{"8.8.8.8:5000", false},
		{"not-an-address", false},
		{"192.168.1.20", false}, // no port
	}
	for _, tt := range tests {
		if got := checker.LocalAddrPort(tt.address); got != tt.want {
			t.Errorf("LocalAddrPort(%q) = %v, want %v", tt.address, got, tt.want)
		}
	}
}

func TestOwnPrefixesAreReadAgainAfterAMinute(t *testing.T) {
	calls := 0
	checker := &Checker{Interfaces: func() ([]net.Addr, error) {
		calls++
		return ownAddresses()
	}}
	addr := netip.MustParseAddr("2001:db8:1:2::20")

	checker.Local(addr)
	checker.Local(addr)
	if calls != 1 {
		t.Fatalf("interfaces read %d times within a minute, want once", calls)
	}

	checker.refreshed = time.Now().Add(-2 * prefixesRefresh)
	checker.Local(addr)
	if calls != 2 {
		t.Errorf("interfaces read %d times after a minute, want twice", calls)
	}
}

func TestOwnPrefixesAreKeptWhenTheInterfacesCannotBeRead(t *testing.T) {
	fail := false
	checker := &Checker{Interfaces: func() ([]net.Addr, error) {
		if fail {
			return nil, errors.New("no interfaces")
		}
		return ownAddresses()
	}}
	addr := netip.MustParseAddr("2001:db8:1:2::20")
	if !checker.Local(addr) {
		t.Fatal("Local() = false with the interfaces readable, want true")
	}

	fail = true
	checker.refreshed = time.Now().Add(-2 * prefixesRefresh)
	if !checker.Local(addr) {
		t.Error("Local() = false after the interfaces failed, want the known subnet kept")
	}
}

func TestPrivateAddressesNeedNoInterfaces(t *testing.T) {
	checker := &Checker{Interfaces: func() ([]net.Addr, error) {
		t.Error("the interfaces were read for a private address")
		return nil, nil
	}}
	if !checker.Local(netip.MustParseAddr("192.168.1.20")) {
		t.Error("Local(192.168.1.20) = false, want true")
	}
}
