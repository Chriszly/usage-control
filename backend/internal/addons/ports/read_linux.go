package ports

import (
	"context"
	"os"
	"path/filepath"
)

// Read returns the ports the machine listens on, from the socket tables of
// the network namespace of process 1 under procDir: the host's, also in a
// container that mounts the host's /proc, and also for a service in a
// network namespace of its own. ok is false when none of the tables could be
// read.
func Read(_ context.Context, procDir string) (ports []Port, ok bool) {
	var sockets []Socket
	for _, table := range []struct{ file, protocol string }{
		{"tcp", "tcp"}, {"tcp6", "tcp"}, {"udp", "udp"}, {"udp6", "udp"},
	} {
		// procDir comes from HOST_PROC, the file names are fixed.
		text, err := os.ReadFile(filepath.Join(procDir, "1", "net", table.file)) //nolint:gosec // see above
		if err != nil {
			continue // tcp6 and udp6 are missing without IPv6
		}
		ok = true
		sockets = append(sockets, parseProcNet(string(text), table.protocol)...)
	}
	return Listening(sockets), ok
}
