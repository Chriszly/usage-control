package metrics

import (
	"encoding/binary"
	"math/bits"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"
)

// linkInterval is how often the speed and addresses of the network
// interfaces are read again. They rarely change, so a minute old is fine.
const linkInterval = time.Minute

// link is the speed an interface is connected at, in Mbit/s (0 when unknown),
// and its IPv4 addresses.
type link struct {
	mbps      int
	addresses []string
}

// addLinks adds the speed and addresses to each interface, read again when
// the last reading is older than linkInterval.
func (c *Collector) addLinks(interfaces []NetworkInterface) {
	c.linkMu.Lock()
	defer c.linkMu.Unlock()
	if now := time.Now(); now.Sub(c.linkTime) >= linkInterval {
		names := make([]string, len(interfaces))
		for i, iface := range interfaces {
			names[i] = iface.Name
		}
		c.links, c.linkTime = readLinks(names), now
	}
	for i, iface := range interfaces {
		l := c.links[iface.Name]
		interfaces[i].LinkMbps, interfaces[i].Addresses = l.mbps, l.addresses
	}
}

// This part reads Linux's routing files. It has no build constraint so its
// tests run on every OS.

// route is one line of /proc/net/route: the network an interface reaches.
type route struct {
	iface   string
	network netip.Prefix
}

// parseRoutes reads the networks each interface reaches from the text of
// /proc/net/route, leaving out default routes. Addresses in it are
// hexadecimal numbers in the byte order of the machine, little-endian on
// every system usage-control runs on.
func parseRoutes(text string) []route {
	var routes []route
	for line := range strings.Lines(text) {
		// Iface Destination Gateway Flags RefCnt Use Metric Mask ...
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		destination, destinationErr := strconv.ParseUint(fields[1], 16, 32)
		mask, maskErr := strconv.ParseUint(fields[7], 16, 32)
		if destinationErr != nil || maskErr != nil || mask == 0 {
			continue
		}
		var address [4]byte
		binary.LittleEndian.PutUint32(address[:], uint32(destination))
		ones := bits.OnesCount32(uint32(mask))
		routes = append(routes, route{iface: fields[0], network: netip.PrefixFrom(netip.AddrFrom4(address), ones)})
	}
	return routes
}

// parseLocalAddresses reads the machine's own IPv4 addresses from the text
// of /proc/net/fib_trie: each address line followed by a "/32 host LOCAL"
// line. Loopback addresses are left out.
func parseLocalAddresses(text string) []netip.Addr {
	var addresses []netip.Addr
	var previous string
	for line := range strings.Lines(text) {
		line = strings.TrimSpace(line)
		if line == "/32 host LOCAL" {
			address, err := netip.ParseAddr(strings.TrimPrefix(previous, "|-- "))
			if err == nil && !address.IsLoopback() && !slices.Contains(addresses, address) {
				addresses = append(addresses, address)
			}
		}
		previous = line
	}
	return addresses
}

// addressesByInterface finds the interface of each local address: the one
// that reaches the address's network.
func addressesByInterface(addresses []netip.Addr, routes []route) map[string][]string {
	result := map[string][]string{}
	for _, address := range addresses {
		for _, r := range routes {
			if r.network.Contains(address) {
				result[r.iface] = append(result[r.iface], address.String())
				break
			}
		}
	}
	return result
}
