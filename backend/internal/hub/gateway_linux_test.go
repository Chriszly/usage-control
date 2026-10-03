package hub

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

func TestParseRoutes(t *testing.T) {
	// /proc/net/route writes each address as a number in the machine's byte
	// order.
	hexOf := func(addr string) string {
		ip := netip.MustParseAddr(addr).As4()
		return fmt.Sprintf("%08X", binary.NativeEndian.Uint32(ip[:]))
	}
	routes := strings.Join([]string{
		"Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT",
		"eth0\t00000000\t" + hexOf("172.17.0.1") + "\t0003\t0\t0\t0\t00000000\t0\t0\t0",
		"eth0\t" + hexOf("172.17.0.0") + "\t00000000\t0001\t0\t0\t0\t" + hexOf("255.255.0.0") + "\t0\t0\t0",
	}, "\n")

	got := parseRoutes(bufio.NewScanner(strings.NewReader(routes)))
	if want := []netip.Addr{netip.MustParseAddr("172.17.0.1")}; !reflect.DeepEqual(got, want) {
		t.Errorf("parseRoutes() = %v, want %v", got, want)
	}
}
