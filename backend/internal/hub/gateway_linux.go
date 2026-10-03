package hub

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"net/netip"
	"os"
	"strings"
)

// defaultGateways reads the IPv4 default gateways from /proc/net/route. In a
// Docker container, that is the gateway of the Docker network, which a
// visitor shows up as when Docker's proxy passes its connection on.
func defaultGateways() []netip.Addr {
	file, err := os.Open("/proc/net/route")
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()
	return parseRoutes(bufio.NewScanner(file))
}

// parseRoutes reads the gateways of the default routes from the lines of
// /proc/net/route: Iface, Destination, Gateway and more columns, with the
// addresses in hexadecimal in the machine's byte order.
func parseRoutes(lines *bufio.Scanner) []netip.Addr {
	var gateways []netip.Addr
	for lines.Scan() {
		fields := strings.Fields(lines.Text())
		if len(fields) < 3 || fields[1] != "00000000" {
			continue
		}
		raw, err := hex.DecodeString(fields[2])
		if err != nil || len(raw) != 4 {
			continue
		}
		var ip [4]byte
		binary.NativeEndian.PutUint32(ip[:], binary.BigEndian.Uint32(raw))
		if gateway := netip.AddrFrom4(ip); !gateway.IsUnspecified() {
			gateways = append(gateways, gateway)
		}
	}
	return gateways
}
