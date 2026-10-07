package ports

import (
	"encoding/binary"
	"encoding/hex"
	"net/netip"
	"strings"
)

// The states of a socket in /proc/net/tcp and /proc/net/udp.
const (
	stateListen = "0A" // TCP_LISTEN
	stateClose  = "07" // TCP_CLOSE: a UDP socket that is not connected
)

// parseProcNet reads the listening sockets of protocol ("tcp" or "udp") from
// one of the kernel's tables, /proc/net/tcp, tcp6, udp or udp6, whose lines
// look like
//
//	0: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000 0 0 12345 ...
//
// with the local and the remote address and port in hexadecimal, then the
// state. TCP sockets listen in the state LISTEN; UDP sockets that are bound
// and not connected have no remote port.
func parseProcNet(text, protocol string) []Socket {
	var sockets []Socket
	for line := range strings.Lines(text) {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		switch protocol {
		case "tcp":
			if fields[3] != stateListen {
				continue
			}
		case "udp":
			if fields[3] != stateClose || !strings.HasSuffix(fields[2], ":0000") {
				continue
			}
		}
		address, port, ok := parseHexAddress(fields[1])
		if !ok || port == 0 {
			continue
		}
		sockets = append(sockets, Socket{Protocol: protocol, Address: address, Port: port})
	}
	return sockets
}

// parseHexAddress reads an address such as "0100007F:0016" (127.0.0.1 port
// 22). The address is in 32-bit words in the machine's byte order, which is
// little-endian on every machine usage-control runs on; the port is plain.
func parseHexAddress(field string) (netip.Addr, uint32, bool) {
	hexAddress, hexPort, found := strings.Cut(field, ":")
	if !found {
		return netip.Addr{}, 0, false
	}
	port, err := hex.DecodeString(hexPort)
	if err != nil || len(port) != 2 {
		return netip.Addr{}, 0, false
	}
	raw, err := hex.DecodeString(hexAddress)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return netip.Addr{}, 0, false
	}
	for word := 0; word < len(raw); word += 4 {
		raw[word], raw[word+1], raw[word+2], raw[word+3] = raw[word+3], raw[word+2], raw[word+1], raw[word]
	}
	address, _ := netip.AddrFromSlice(raw)
	return address, uint32(binary.BigEndian.Uint16(port)), true
}
