package ports

import (
	"net/netip"
	"reflect"
	"testing"

	"github.com/shirou/gopsutil/v4/net"
)

const procNetTCP = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 18012 1 0000000000000000 100 0 0 10 0
   1: 0100007F:0277 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 19033 1 0000000000000000 100 0 0 10 0
   2: 0A01A8C0:0016 1401A8C0:D431 01 00000000:00000000 02:000A7B2E 00000000     0        0 52011 4 0000000000000000 20 4 29 10 -1
`

const procNetTCP6 = `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000000000000:0016 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 18014 1 0000000000000000 100 0 0 10 0
   1: 00000000000000000000000001000000:2491 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000   999        0 23111 1 0000000000000000 100 0 0 10 0
   2: 0000000000000000FFFF00000100007F:0277 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 19035 1 0000000000000000 100 0 0 10 0
`

const procNetUDP = `   sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode ref pointer drops
  521: 00000000:14E9 00000000:0000 07 00000000:00000000 00:00000000 00000000   104        0 17020 2 0000000000000000 0
  830: 3500007F:0035 00000000:0000 07 00000000:00000000 00:00000000 00000000   101        0 16890 2 0000000000000000 0
  901: 0A01A8C0:A0F3 0101A8C0:0035 01 00000000:00000000 00:00000000 00000000  1000        0 61002 2 0000000000000000 0
`

func TestParseProcNetKeepsListeningSockets(t *testing.T) {
	got := parseProcNet(procNetTCP, "tcp")
	want := []Socket{
		{Protocol: "tcp", Address: netip.MustParseAddr("0.0.0.0"), Port: 22},
		{Protocol: "tcp", Address: netip.MustParseAddr("127.0.0.1"), Port: 631},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseProcNet(tcp) = %v, want %v", got, want)
	}

	got = parseProcNet(procNetTCP6, "tcp")
	want = []Socket{
		{Protocol: "tcp", Address: netip.MustParseAddr("::"), Port: 22},
		{Protocol: "tcp", Address: netip.MustParseAddr("::1"), Port: 9361},
		{Protocol: "tcp", Address: netip.MustParseAddr("::ffff:127.0.0.1"), Port: 631},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseProcNet(tcp6) = %v, want %v", got, want)
	}

	got = parseProcNet(procNetUDP, "udp")
	want = []Socket{
		{Protocol: "udp", Address: netip.MustParseAddr("0.0.0.0"), Port: 5353},
		{Protocol: "udp", Address: netip.MustParseAddr("127.0.0.53"), Port: 53},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseProcNet(udp) = %v, want %v (without the connected socket)", got, want)
	}
}

func TestListeningMergesIPv4AndIPv6(t *testing.T) {
	var sockets []Socket
	sockets = append(sockets, parseProcNet(procNetUDP, "udp")...)
	sockets = append(sockets, parseProcNet(procNetTCP6, "tcp")...)
	sockets = append(sockets, parseProcNet(procNetTCP, "tcp")...)

	got := Listening(sockets)

	want := []Port{
		{Protocol: "tcp", Number: 22, Addresses: []string{"0.0.0.0", "::"}},
		{Protocol: "tcp", Number: 631, Addresses: []string{"127.0.0.1"}},
		{Protocol: "tcp", Number: 9361, Addresses: []string{"::1"}},
		{Protocol: "udp", Number: 53, Addresses: []string{"127.0.0.53"}},
		{Protocol: "udp", Number: 5353, Addresses: []string{"0.0.0.0"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Listening() = %v, want %v", got, want)
	}
}

func TestFromConnectionsKeepsListeningSockets(t *testing.T) {
	connections := []net.ConnectionStat{
		{Type: sockStream, Status: "LISTEN", Laddr: net.Addr{IP: "0.0.0.0", Port: 135}},
		{Type: sockStream, Status: "ESTABLISHED", Laddr: net.Addr{IP: "192.168.1.5", Port: 50123}, Raddr: net.Addr{IP: "192.168.1.10", Port: 9393}},
		{Type: sockDgram, Laddr: net.Addr{IP: "::", Port: 5353}},
		{Type: sockDgram, Laddr: net.Addr{IP: "bad", Port: 1}},
	}

	got := fromConnections(connections)

	want := []Socket{
		{Protocol: "tcp", Address: netip.MustParseAddr("0.0.0.0"), Port: 135},
		{Protocol: "udp", Address: netip.MustParseAddr("::"), Port: 5353},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fromConnections() = %v, want %v", got, want)
	}
}

func TestExtrasCountsAndListsThePorts(t *testing.T) {
	if got := Extras(nil, false); got != nil {
		t.Errorf("Extras() without tables = %v, want nothing", got)
	}

	ports := []Port{{Protocol: "tcp", Number: 22, Addresses: []string{"0.0.0.0", "::"}}}
	for number := uint32(1000); number < 1070; number++ {
		ports = append(ports, Port{Protocol: "udp", Number: number, Addresses: []string{"0.0.0.0"}})
	}
	groups := Extras(ports, true)

	if len(groups) != 1 || groups[0].ID != "ports" {
		t.Fatalf("Extras() = %+v, want the ports group", groups)
	}
	items := groups[0].Items
	if len(items) != maxItems {
		t.Fatalf("Extras() has %d items, want %d", len(items), maxItems)
	}
	if items[0].ID != "count" || *items[0].Value != 71 || !items[0].History {
		t.Errorf("first item = %+v, want the count 71 with history", items[0])
	}
	if items[1].ID != "tcp-22" || items[1].Label != "TCP 22" || items[1].Text != "0.0.0.0, ::" || items[1].History {
		t.Errorf("second item = %+v, want TCP 22 on 0.0.0.0, :: without history", items[1])
	}
}
