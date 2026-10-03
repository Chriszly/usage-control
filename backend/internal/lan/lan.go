// Package lan tells which addresses belong to the local network, so the
// website only answers it and the hub only talks to it.
package lan

import (
	"net"
	"net/netip"
	"sync"
	"time"
)

// prefixesRefresh is how long the machine's own IPv6 subnets are kept before
// the network interfaces are read again; a provider may hand out a new prefix
// every day.
const prefixesRefresh = time.Minute

// Checker decides whether an address is on the local network. Loopback,
// private (RFC 1918 and IPv6 unique local) and link-local addresses always
// are. A global IPv6 address is too when it lies in the subnet of one of the
// machine's own addresses: devices on a home network with IPv6 from the
// provider carry such global addresses, not private ones.
type Checker struct {
	// Interfaces lists the machine's addresses; net.InterfaceAddrs when nil.
	Interfaces func() ([]net.Addr, error)

	mu        sync.Mutex
	prefixes  []netip.Prefix
	refreshed time.Time
}

// Default checks against the machine's own network interfaces.
var Default = &Checker{}

// Local reports whether addr is on the local network.
func (c *Checker) Local(addr netip.Addr) bool {
	addr = addr.Unmap()
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() {
		return true
	}
	if !addr.Is6() || !addr.IsGlobalUnicast() {
		return false
	}
	for _, prefix := range c.ownPrefixes() {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// LocalAddrPort reports whether the address of a connection, as host:port, is
// on the local network. An address that cannot be read is not.
func (c *Checker) LocalAddrPort(address string) bool {
	addrPort, err := netip.ParseAddrPort(address)
	if err != nil {
		return false
	}
	return c.Local(addrPort.Addr())
}

// ownPrefixes returns the subnets of the machine's global IPv6 addresses,
// reading the interfaces at most once per prefixesRefresh.
func (c *Checker) ownPrefixes() []netip.Prefix {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.refreshed) < prefixesRefresh {
		return c.prefixes
	}
	list := c.Interfaces
	if list == nil {
		list = net.InterfaceAddrs
	}
	addrs, err := list()
	if err != nil {
		// Keep what is known; the next check reads the interfaces again.
		return c.prefixes
	}
	c.prefixes = globalPrefixes(addrs)
	c.refreshed = time.Now()
	return c.prefixes
}

// globalPrefixes returns the subnets of the global IPv6 addresses among addrs.
func globalPrefixes(addrs []net.Addr) []netip.Prefix {
	var prefixes []netip.Prefix
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		addr, ok := netip.AddrFromSlice(ipNet.IP)
		if !ok {
			continue
		}
		addr = addr.Unmap()
		bits, _ := ipNet.Mask.Size()
		if !addr.Is6() || !addr.IsGlobalUnicast() || bits == 0 {
			continue
		}
		prefixes = append(prefixes, netip.PrefixFrom(addr, bits).Masked())
	}
	return prefixes
}
