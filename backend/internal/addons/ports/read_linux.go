package ports

import (
	"context"
	"os"
	"path/filepath"
)

// linuxEphemeral is the range Linux picks ports from by default.
var linuxEphemeral = portRange{32768, 60999}

// read returns the ports the machine listens on, from the socket tables of
// the network namespace of process 1 under procDir: the host's, also in a
// container that mounts the host's /proc. It fails with the first error when
// none of the tables can be read.
func read(_ context.Context, procDir string) ([]Port, error) {
	var sockets []Socket
	var failed error
	found := false
	for _, table := range []struct{ file, protocol string }{
		{"tcp", "tcp"}, {"tcp6", "tcp"}, {"udp", "udp"}, {"udp6", "udp"},
	} {
		// procDir comes from HOST_PROC, the file names are fixed.
		text, err := os.ReadFile(filepath.Join(procDir, "1", "net", table.file)) //nolint:gosec // see above
		if err != nil {
			// tcp6 and udp6 are missing without IPv6.
			if failed == nil {
				failed = err
			}
			continue
		}
		found = true
		sockets = append(sockets, parseProcNet(string(text), table.protocol)...)
	}
	if !found {
		return nil, failed
	}
	return Listening(withoutEphemeralUDP(sockets, ephemeralRange(procDir))), nil
}

// ephemeralRange returns the range Linux picks ports from, from
// /proc/sys/net/ipv4/ip_local_port_range under procDir, or its default when
// that cannot be read. The range belongs to a network namespace and is read
// from the add-on's own: the Linux service runs in the host's, so it reads
// the host's range, while the container (network_mode: none) has one of its
// own, which keeps the default even when the host's was changed.
func ephemeralRange(procDir string) portRange {
	// procDir comes from HOST_PROC, the file name is fixed.
	text, err := os.ReadFile(filepath.Join(procDir, "sys", "net", "ipv4", "ip_local_port_range")) //nolint:gosec // see above
	if err != nil {
		return linuxEphemeral
	}
	return parsePortRange(string(text), linuxEphemeral)
}
