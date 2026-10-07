// Package ports reads the ports the machine listens on, for the ports
// add-on: each TCP port that accepts connections and each UDP port a program
// has bound outside the range the system hands out to sockets that only
// send, with the addresses it listens on. On Linux it reads the kernel's
// socket tables in /proc, on Windows the tables Windows keeps through
// gopsutil.
//
// It only reads; nothing in here changes the machine.
package ports

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// Reader reads the ports the machine listens on and logs when it cannot:
// once when that starts and once when it works again.
type Reader struct {
	procDir string
	failing bool
}

// NewReader returns a Reader that reads the socket tables under procDir, on
// Linux (see HostProc).
func NewReader(procDir string) *Reader {
	return &Reader{procDir: procDir}
}

// Read returns the ports the machine listens on, sorted by protocol and
// then by number. ok is false when they could not be read.
func (r *Reader) Read(ctx context.Context) (ports []Port, ok bool) {
	ports, err := read(ctx, r.procDir)
	switch {
	case err != nil && !r.failing:
		slog.Warn("read the ports", "error", err)
	case err == nil && r.failing:
		slog.Info("reading the ports works again")
	}
	r.failing = err != nil
	return ports, err == nil
}

// Socket is one socket that listens: TCP in the LISTEN state, or UDP bound to
// a port without a remote end.
type Socket struct {
	// Protocol is "tcp" or "udp".
	Protocol string
	Address  netip.Addr
	Port     uint32
}

// portRange is a range of port numbers, first and last included.
type portRange struct{ first, last uint32 }

// parsePortRange reads a range written as two numbers, such as
// "32768\t60999" in /proc/sys/net/ipv4/ip_local_port_range, or returns
// fallback when the text is not one.
func parsePortRange(text string, fallback portRange) portRange {
	fields := strings.Fields(text)
	if len(fields) != 2 {
		return fallback
	}
	first, err1 := strconv.ParseUint(fields[0], 10, 16)
	last, err2 := strconv.ParseUint(fields[1], 10, 16)
	if err1 != nil || err2 != nil || first == 0 || first > last {
		return fallback
	}
	return portRange{uint32(first), uint32(last)}
}

// withoutEphemeralUDP returns sockets without the UDP sockets bound to a
// port in ephemeral, the range the system picks ports from for sockets that
// do not ask for one. Programs open those to send for a moment, such as a
// DNS lookup, a browser's QUIC or WebRTC, and they would fill the list with
// random ports that come and go. A service that listens on UDP in that range
// is left out with them.
func withoutEphemeralUDP(sockets []Socket, ephemeral portRange) []Socket {
	return slices.DeleteFunc(sockets, func(s Socket) bool {
		return s.Protocol == "udp" && s.Port >= ephemeral.first && s.Port <= ephemeral.last
	})
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
			Text:  joinAddresses(p.Addresses),
		})
	}
	return []metrics.Extra{group}
}

// maxTextLength is the most characters a text of extras keeps.
const maxTextLength = 80

// joinAddresses returns addresses as one text of at most maxTextLength
// characters: as many whole addresses as fit, then how many more there are,
// such as "10.0.0.1, 10.0.0.2 +3", so no address is cut in the middle.
func joinAddresses(addresses []string) string {
	if text := strings.Join(addresses, ", "); len(text) <= maxTextLength {
		return text
	}
	text := ""
	for i, address := range addresses {
		next := address
		if i > 0 {
			next = text + ", " + address
		}
		if len(next)+len(fmt.Sprintf(" +%d", len(addresses)-i-1)) > maxTextLength {
			return fmt.Sprintf("%s +%d", text, len(addresses)-i)
		}
		text = next
	}
	return text
}

// HostProc returns where /proc is: HOST_PROC in a container that mounts the
// host's /proc there, else /proc.
func HostProc() string {
	if dir := os.Getenv("HOST_PROC"); dir != "" {
		return dir
	}
	return "/proc"
}
