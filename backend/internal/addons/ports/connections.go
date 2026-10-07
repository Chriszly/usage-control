package ports

import (
	"net/netip"

	"github.com/shirou/gopsutil/v4/net"
)

// The socket types gopsutil reports, as in syscall.
const (
	sockStream = 1
	sockDgram  = 2
)

// fromConnections returns the listening sockets among what gopsutil reports:
// TCP in the state LISTEN, and UDP without a remote port.
func fromConnections(connections []net.ConnectionStat) []Socket {
	var sockets []Socket
	for _, c := range connections {
		var protocol string
		switch {
		case c.Type == sockStream && c.Status == "LISTEN":
			protocol = "tcp"
		case c.Type == sockDgram && c.Raddr.Port == 0:
			protocol = "udp"
		default:
			continue
		}
		address, err := netip.ParseAddr(c.Laddr.IP)
		if err != nil || c.Laddr.Port == 0 || c.Laddr.Port > 65535 {
			continue
		}
		sockets = append(sockets, Socket{Protocol: protocol, Address: address, Port: c.Laddr.Port})
	}
	return sockets
}
