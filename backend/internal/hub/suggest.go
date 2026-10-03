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
	// askName asks the usage-control at address what the device calls itself.
	askName func(ctx context.Context, address string) (string, error)
	// lookupAddr and lookupHost are reverse and forward DNS lookups.
	lookupAddr func(ctx context.Context, addr string) ([]string, error)
	lookupHost func(ctx context.Context, host string) ([]string, error)
}

func defaultSuggester() suggester {
	return suggester{
		port:     DefaultPort,
		ownAddrs: net.InterfaceAddrs,
		gateways: defaultGateways,
		askName: func(ctx context.Context, address string) (string, error) {
			snapshot, err := NewAgent(address).ask(ctx)
			return snapshot.Name, err
		},
		lookupAddr: net.DefaultResolver.LookupAddr,
		lookupHost: net.DefaultResolver.LookupHost,
	}
}

// Suggest returns the device at from, the address of a visitor of the page,
// for the page to offer adding it. There is none when from is this machine,
// one of its gateways or a device the hub already collects from. Behind
// Docker's port publishing, a visitor can show up with the address of the
// Docker network's gateway instead of its own, which is not suggested either.
func (h *Hub) Suggest(ctx context.Context, from netip.Addr) (Suggestion, bool) {
	var known []Device
	for _, r := range h.Remotes() {
		known = append(known, r.Device)
	}
	return h.suggester.suggest(ctx, from, known)
}

func (s suggester) suggest(ctx context.Context, from netip.Addr, known []Device) (Suggestion, bool) {
	from = from.Unmap().WithZone("")
	// A link-local IPv6 address only works with its zone, which an address
	// on the page cannot carry.
	if !from.IsValid() || from.IsLoopback() || from.IsUnspecified() || (from.Is6() && from.IsLinkLocalUnicast()) {
		return Suggestion{}, false
	}
	if s.isOwn(from) {
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
		knownAddress     = make([]bool, len(known))
	)
	wait.Go(func() {
		if name, err := s.askName(ctx, address); err == nil {
			ownName = name
		}
	})
	wait.Go(func() {
		if names, err := s.lookupAddr(ctx, from.String()); err == nil && len(names) > 0 {
			dnsName = firstLabel(names[0])
		}
	})
	for i, device := range known {
		wait.Go(func() { knownAddress[i] = s.reaches(ctx, device.Address, from) })
	}
	wait.Wait()

	for i, device := range known {
		if knownAddress[i] {
			return Suggestion{}, false
		}
		// A device added under a name that does not resolve here is still
		// recognized by what it calls itself.
		if ownName != "" && idOf(ownName) == device.ID {
			return Suggestion{}, false
		}
	}
	name := usableName(ownName)
	if name == "" {
		name = usableName(dnsName)
	}
	return Suggestion{Address: address, Name: name}, true
}

// isOwn reports whether addr is one of this machine's addresses.
func (s suggester) isOwn(addr netip.Addr) bool {
	addrs, err := s.ownAddrs()
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

// reaches reports whether a device's address (host:port) points at addr,
// looking a host name up in the DNS.
func (s suggester) reaches(ctx context.Context, address string, addr netip.Addr) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Unmap() == addr
	}
	ips, err := s.lookupHost(ctx, host)
	if err != nil {
		return false
	}
	for _, found := range ips {
		if ip, err := netip.ParseAddr(found); err == nil && ip.Unmap().WithZone("") == addr {
			return true
		}
	}
	return false
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
