package metrics

import (
	"encoding/hex"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/Chriszly/usage-control/backend/internal/sysfile"
)

// HostAddresses returns every address of the host in a container that
// mounts the host's /proc as HOST_PROC: those of its network cards and of
// its virtual interfaces too, such as Docker's bridges and a VPN, read from
// the routing files of the host's first process. Outside a container it
// returns nil, as the machine's own network interfaces are the host's.
// When the host's IPv4 addresses cannot be read, as with hidepid or a missing
// mount, it says so in the log once.
func HostAddresses() []netip.Addr {
	proc := os.Getenv("HOST_PROC")
	if proc == "" {
		return nil
	}
	netDir := filepath.Join(proc, "1", "net")
	var addresses []netip.Addr
	if trie, err := sysfile.Read(filepath.Join(netDir, "fib_trie")); err == nil {
		addresses = parseLocalAddresses(string(trie))
	} else if !hostAddressesUnread.Swap(true) {
		slog.Warn("read the host's addresses; a device at one of them, such as Docker's bridge gateway, can be added on the page", "error", err)
	}
	if inet6, err := sysfile.Read(filepath.Join(netDir, "if_inet6")); err == nil {
		for _, address := range parseIPv6Addresses(string(inet6)) {
			if !slices.Contains(addresses, address) {
				addresses = append(addresses, address)
			}
		}
	}
	return addresses
}

// hostAddressesUnread is set once the log said that the host's addresses
// cannot be read.
var hostAddressesUnread atomic.Bool

// parseIPv6Addresses reads the machine's IPv6 addresses from the text of
// /proc/net/if_inet6, whose lines start with the address as 32 hex digits.
func parseIPv6Addresses(text string) []netip.Addr {
	var addresses []netip.Addr
	for line := range strings.Lines(text) {
		field, _, _ := strings.Cut(strings.TrimSpace(line), " ")
		raw, err := hex.DecodeString(field)
		if err != nil || len(raw) != 16 {
			continue
		}
		address := netip.AddrFrom16([16]byte(raw))
		if !slices.Contains(addresses, address) {
			addresses = append(addresses, address)
		}
	}
	return addresses
}
