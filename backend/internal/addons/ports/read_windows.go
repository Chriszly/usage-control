package ports

import (
	"context"

	"github.com/shirou/gopsutil/v4/net"
)

// Read returns the ports the machine listens on, from the TCP and UDP tables
// Windows keeps, which any account may read. procDir is not used. ok is false
// when Windows does not answer.
func Read(ctx context.Context, _ string) (ports []Port, ok bool) {
	connections, err := net.ConnectionsWithContext(ctx, "inet")
	if err != nil {
		return nil, false
	}
	return Listening(fromConnections(connections)), true
}
