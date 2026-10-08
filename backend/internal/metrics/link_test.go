package metrics

import (
	"net/netip"
	"reflect"
	"testing"
)

func TestAddressesByInterface(t *testing.T) {
	routes := `Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
eth0	00000000	013CA8C0	0003	0	0	100	00000000	0	0	0
eth0	003CA8C0	00000000	0001	0	0	100	00FFFFFF	0	0	0
wlan0	0000000A	00000000	0001	0	0	600	000000FF	0	0	0
`
	trie := `Main:
  +-- 0.0.0.0/0 3 0 5
     +-- 127.0.0.0/8 2 0 2
           |-- 127.0.0.1
              /32 host LOCAL
     +-- 192.168.60.0/24 2 0 2
           |-- 192.168.60.0
              /24 link UNICAST
           |-- 192.168.60.9
              /32 host LOCAL
        |-- 192.168.60.255
           /32 link BROADCAST
     +-- 10.0.0.0/8 2 0 2
           |-- 10.1.2.3
              /32 host LOCAL
Local:
     +-- 192.168.60.0/24 2 0 2
           |-- 192.168.60.9
              /32 host LOCAL
`
	got := addressesByInterface(parseLocalAddresses(trie), parseRoutes(routes))
	want := map[string][]string{"eth0": {"192.168.60.9"}, "wlan0": {"10.1.2.3"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("addressesByInterface() = %v, want %v", got, want)
	}
}

func TestAddressesByInterfacePicksTheMostSpecificRoute(t *testing.T) {
	// A VPN's split route to 192.168.0.0/16 comes before the network card's
	// own 192.168.60.0/24.
	routes := []route{
		{iface: "tun0", network: netip.MustParsePrefix("192.168.0.0/16")},
		{iface: "eth0", network: netip.MustParsePrefix("192.168.60.0/24")},
		{iface: "tun0", network: netip.MustParsePrefix("10.8.0.0/24")},
	}
	addresses := []netip.Addr{netip.MustParseAddr("192.168.60.9"), netip.MustParseAddr("10.8.0.2")}

	got := addressesByInterface(addresses, routes)

	want := map[string][]string{"eth0": {"192.168.60.9"}, "tun0": {"10.8.0.2"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("addressesByInterface() = %v, want %v", got, want)
	}
}

func TestAddressesByInterfacePrefersTheLinkToAGatewayRoute(t *testing.T) {
	// A VPN pushes 192.168.60.0/25 through its gateway 10.8.0.1, narrower
	// than the network card's own link 192.168.60.0/24.
	routes := `Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
eth0	003CA8C0	00000000	0001	0	0	100	00FFFFFF	0	0	0
tun0	003CA8C0	0100080A	0003	0	0	0	80FFFFFF	0	0	0
tun0	0000080A	00000000	0001	0	0	0	00FFFFFF	0	0	0
`
	addresses := []netip.Addr{netip.MustParseAddr("192.168.60.9"), netip.MustParseAddr("10.8.0.2")}

	got := addressesByInterface(addresses, parseRoutes(routes))

	want := map[string][]string{"eth0": {"192.168.60.9"}, "tun0": {"10.8.0.2"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("addressesByInterface() = %v, want %v", got, want)
	}
}

func TestAddLinksReadsAgainOnlyAfterTheInterval(t *testing.T) {
	c := &Collector{}
	interfaces := []NetworkInterface{{Name: "eth0"}}
	c.addLinks(interfaces)
	read := c.linkTime
	c.links = map[string]link{"eth0": {mbps: 1000, addresses: []string{"192.168.60.9"}}}

	c.addLinks(interfaces)

	if c.linkTime != read {
		t.Error("addLinks() read the links again within linkInterval")
	}
	if interfaces[0].LinkMbps != 1000 || !reflect.DeepEqual(interfaces[0].Addresses, []string{"192.168.60.9"}) {
		t.Errorf("interface = %+v, want the kept speed and address", interfaces[0])
	}
}
