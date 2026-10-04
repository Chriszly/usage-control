package hub

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// DefaultPort is the port usage-control listens on unless it is set up
// otherwise.
const DefaultPort = 9393

// suggestTimeout is how long working out a suggestion may take: asking the
// device and the reverse DNS lookup run side by side within it.
const suggestTimeout = 2 * time.Second

// Suggestion is a device the page offers to add: the one that opened it.
type Suggestion struct {
	// Address is where its usage-control would be reachable, as host:port.
	Address string `json:"address"`
	// Name is what the device calls itself, or its name in the local DNS;
	// empty when neither is known.
	Name string `json:"name"`
	// Kind is what the device seems to be from its usage: a PC when it has a
	// battery or runs Windows, else a server. The visitor picks the kind.
	Kind Kind `json:"kind"`
}

// suggester works out the Suggestion for an address. The fields are the
// lookups it makes, which tests replace.
type suggester struct {
	// port is the port the suggested address gets and the device is asked on.
	port int
	// ownAddrs lists the machine's addresses; net.InterfaceAddrs when nil.
	ownAddrs func() ([]net.Addr, error)
	// gateways lists the default gateways of the machine's network.
	gateways func() []netip.Addr
	// ask asks the usage-control at address for its usage.
	ask func(ctx context.Context, address string) (metrics.Snapshot, error)
	// lookupAddr and lookupHost are reverse and forward DNS lookups.
	lookupAddr func(ctx context.Context, addr string) ([]string, error)
	lookupHost func(ctx context.Context, host string) ([]string, error)
}

func defaultSuggester() suggester {
	return suggester{
		port:       DefaultPort,
		ownAddrs:   net.InterfaceAddrs,
		gateways:   defaultGateways,
		ask:        askOnce,
		lookupAddr: net.DefaultResolver.LookupAddr,
		lookupHost: net.DefaultResolver.LookupHost,
	}
}

// Suggest returns the device at from, the address of a visitor of the page,
// for the page to offer adding it. own lists more addresses of this machine
// besides its network interfaces' own: in a container, those of the host,
// which its usage reading lists. There is none when from is this machine,
// one of its gateways or a device the hub already collects from at that
// address and the default port. Behind
// Docker's port publishing, a visitor can show up with the address of the
// Docker network's gateway instead of its own, which is not suggested either.
func (h *Hub) Suggest(ctx context.Context, from netip.Addr, own []netip.Addr) (Suggestion, bool) {
	var known []Device
	for _, r := range h.Remotes() {
		known = append(known, r.Device)
	}
	return h.suggester.suggest(ctx, from, known, own)
}

func (s suggester) suggest(ctx context.Context, from netip.Addr, known []Device, own []netip.Addr) (Suggestion, bool) {
	from = from.Unmap().WithZone("")
	// A link-local IPv6 address only works with its zone, which an address
	// on the page cannot carry.
	if !from.IsValid() || from.IsLoopback() || from.IsUnspecified() || (from.Is6() && from.IsLinkLocalUnicast()) {
		return Suggestion{}, false
	}
	if s.isOwn(from, own) {
		return Suggestion{}, false
	}
	for _, gateway := range s.gateways() {
		if gateway == from {
			return Suggestion{}, false
		}
	}

	ctx, cancel := context.WithTimeout(ctx, suggestTimeout)
	defer cancel()
	address := net.JoinHostPort(from.String(), strconv.Itoa(s.port))

	// Asking the device, the reverse lookup and the lookups of the devices
	// added by name all wait on the network, so they run side by side.
	var (
		wait             sync.WaitGroup
		ownName, dnsName string
		kind             = KindServer
		knownAddress     = make([]bool, len(known))
		knownChecked     = make([]bool, len(known))
	)
	wait.Go(func() {
		if snapshot, err := s.ask(ctx, address); err == nil {
			ownName, kind = snapshot.Name, kindOf(snapshot)
		}
	})
	wait.Go(func() {
		if names, err := s.lookupAddr(ctx, from.String()); err == nil && len(names) > 0 {
			dnsName = firstLabel(names[0])
		}
	})
	for i, device := range known {
		wait.Go(func() { knownAddress[i], knownChecked[i] = s.reaches(ctx, device.Address, from) })
	}
	wait.Wait()

	taken := make(map[string]bool, len(known))
	for i, device := range known {
		if knownAddress[i] {
			return Suggestion{}, false
		}
		// A device added under a name that does not resolve here is still
		// recognized by what it calls itself. When its address could be
		// checked, the visitor is another device with the same name, such as
		// a second Pi called raspberrypi.
		if !knownChecked[i] && ownName != "" && idOf(ownName) == device.ID {
			return Suggestion{}, false
		}
		taken[device.ID] = true
	}
	// A name another device has is left out, for the visitor to fill in.
	name := usableName(ownName)
	if name == "" || taken[idOf(name)] {
		name = usableName(dnsName)
	}
	if taken[idOf(name)] {
		name = ""
	}
	return Suggestion{Address: address, Name: name, Kind: kind}, true
}

// isOwn reports whether addr is one of this machine's addresses: one of its
// network interfaces' or one of own.
func (s suggester) isOwn(addr netip.Addr, own []netip.Addr) bool {
	for _, o := range own {
		if o.Unmap().WithZone("") == addr {
			return true
		}
	}
	ownAddrs := s.ownAddrs
	if ownAddrs == nil {
		ownAddrs = net.InterfaceAddrs
	}
	addrs, err := ownAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if own, ok := netip.AddrFromSlice(ipNet.IP); ok && own.Unmap() == addr {
			return true
		}
	}
	return false
}

// reaches reports whether a device's address (host:port) points at addr
// with the suggested port, looking a host name up in the DNS. The same address with
// another port is another device. checked is false when the address's host
// name does not resolve here, so it could not be told.
func (s suggester) reaches(ctx context.Context, address string, addr netip.Addr) (reaches, checked bool) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return false, false
	}
	if p, err := strconv.Atoi(portText); err != nil || p != s.port {
		return false, true
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Unmap() == addr, true
	}
	ips, err := s.lookupHost(ctx, host)
	if err != nil {
		return false, false
	}
	for _, found := range ips {
		if ip, err := netip.ParseAddr(found); err == nil && ip.Unmap().WithZone("") == addr {
			return true, true
		}
	}
	return false, true
}

// firstLabel returns the host part of a DNS name, such as office-pc for
// office-pc.fritz.box.
func firstLabel(name string) string {
	label, _, _ := strings.Cut(strings.TrimSuffix(name, "."), ".")
	return label
}

// usableName returns name when a device can be added under it, or "" when
// it cannot.
func usableName(name string) string {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > maxNameLength {
		return ""
	}
	if id := idOf(name); id == "" || id == LocalID {
		return ""
	}
	return name
}
