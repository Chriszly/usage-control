package ports

import (
	"context"

	"github.com/shirou/gopsutil/v4/net"
)

// windowsEphemeral is the range Windows picks ports from by default, as
// "netsh int ipv4 show dynamicport udp" shows it. No simple call tells a
// changed range, so the default is taken.
var windowsEphemeral = portRange{49152, 65535}

// read returns the ports the machine listens on, from the TCP and UDP tables
// Windows keeps, which any account may read. procDir is not used. Windows'
// UDP table has no remote ends, so every UDP socket outside the ephemeral
// range counts.
func read(ctx context.Context, _ string) ([]Port, error) {
	connections, err := net.ConnectionsWithContext(ctx, "inet")
	if err != nil {
		return nil, err
	}
	return Listening(withoutEphemeralUDP(fromConnections(connections), windowsEphemeral)), nil
}
