// Package ports reads the ports the machine listens on, for the ports
// add-on: each TCP port that accepts connections and each UDP port a program
// has bound, with the addresses it listens on. On Linux it reads the kernel's
// socket tables in /proc, on Windows the tables Windows keeps through
// gopsutil.
//
// It only reads; nothing in here changes the machine.
package ports

import (
	"cmp"
	"fmt"
	"net/netip"
	"os"
	"slices"
	"strings"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// Socket is one socket that listens: TCP in the LISTEN state, or UDP bound to
// a port without a remote end.
type Socket struct {
	// Protocol is "tcp" or "udp".
	Protocol string
	Address  netip.Addr
	Port     uint32
}

// Port is a port the machine listens on, over IPv4 and IPv6 together.
type Port struct {
	Protocol string
	Number   uint32
	// Addresses are the addresses it listens on, IPv4 first, such as
	// "0.0.0.0" and "::" for all of them.
	Addresses []string
}

// Listening returns the ports of sockets, each once, sorted by protocol and
// then by number.
func Listening(sockets []Socket) []Port {
	type key struct {
		protocol string
		number   uint32
	}
	addresses := map[key][]netip.Addr{}
	for _, s := range sockets {
		k := key{s.Protocol, s.Port}
		address := s.Address.Unmap()
		if !slices.Contains(addresses[k], address) {
			addresses[k] = append(addresses[k], address)
		}
	}
	ports := make([]Port, 0, len(addresses))
	for k, list := range addresses {
		// IPv4 sorts before IPv6.
		slices.SortFunc(list, func(a, b netip.Addr) int { return a.Compare(b) })
		texts := make([]string, len(list))
		for i, address := range list {
			texts[i] = address.String()
		}
		ports = append(ports, Port{Protocol: k.protocol, Number: k.number, Addresses: texts})
	}
	slices.SortFunc(ports, func(a, b Port) int {
		return cmp.Or(cmp.Compare(a.Protocol, b.Protocol), cmp.Compare(a.Number, b.Number))
	})
	return ports
}

// maxItems is the most values a group of extras keeps (HISTORY_MAX_ENTRIES
// by default); the first is the number of ports.
const maxItems = 64

// Extras returns the ports as the group of extras the collector shows: how
// many there are, which a hub keeps in its history, then each port with its
// addresses, shown live only. It returns nothing when the ports could not be
// read (ok is false).
func Extras(ports []Port, ok bool) []metrics.Extra {
	if !ok {
		return nil
	}
	count := float64(len(ports))
	labels := map[string]string{"de": "Offene Ports", "fr": "Ports en écoute", "es": "Puertos en escucha"}
	group := metrics.Extra{
		ID:     "ports",
		Title:  "Listening ports",
		Titles: labels,
		Items: []metrics.ExtraItem{{
			ID:      "count",
			Label:   "Listening ports",
			Labels:  labels,
			Unit:    metrics.UnitNumber,
			Value:   &count,
			History: true,
		}},
	}
	for _, p := range ports {
		if len(group.Items) == maxItems {
			break
		}
		group.Items = append(group.Items, metrics.ExtraItem{
			ID:    fmt.Sprintf("%s-%d", p.Protocol, p.Number),
			Label: fmt.Sprintf("%s %d", strings.ToUpper(p.Protocol), p.Number),
			Unit:  metrics.UnitText,
			Text:  strings.Join(p.Addresses, ", "),
		})
	}
	return []metrics.Extra{group}
}

// HostProc returns where /proc is: HOST_PROC in a container that mounts the
// host's /proc there, else /proc.
func HostProc() string {
	if dir := os.Getenv("HOST_PROC"); dir != "" {
		return dir
	}
	return "/proc"
}
