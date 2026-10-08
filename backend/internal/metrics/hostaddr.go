package metrics

import (
	"encoding/hex"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Chriszly/usage-control/backend/internal/sysfile"
)

// HostAddresses returns every address of the host in a container that
// mounts the host's /proc as HOST_PROC: those of its network cards and of
// its virtual interfaces too, such as Docker's bridges and a VPN, read from
// the routing files of the host's first process. Outside a container it
// returns nil, as the machine's own network interfaces are the host's.
func HostAddresses() []netip.Addr {
	proc := os.Getenv("HOST_PROC")
	if proc == "" {
		return nil
	}
	netDir := filepath.Join(proc, "1", "net")
	var addresses []netip.Addr
	if trie, err := sysfile.Read(filepath.Join(netDir, "fib_trie")); err == nil {
		addresses = parseLocalAddresses(string(trie))
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
