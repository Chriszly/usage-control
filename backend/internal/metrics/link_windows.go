package metrics

import (
	"math"
	stdnet "net"

	"golang.org/x/sys/windows"
)

// readLinks asks Windows for the speed and IPv4 addresses of each interface.
func readLinks(names []string) map[string]link {
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	interfaces, err := stdnet.Interfaces()
	if err != nil {
		return nil
	}
	links := make(map[string]link, len(names))
	for _, iface := range interfaces {
		if !wanted[iface.Name] || iface.Index < 0 || iface.Index > math.MaxUint32 {
			continue
		}
		var l link
		row := windows.MibIfRow2{InterfaceIndex: uint32(iface.Index)}
		// An unknown speed is reported as the largest number, so speeds
		// too large to be real are left out.
		if windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &row) == nil {
			if mbps := row.ReceiveLinkSpeed / 1_000_000; mbps <= math.MaxInt32 {
				l.mbps = int(mbps)
			}
		}
		addresses, _ := iface.Addrs()
		for _, a := range addresses {
			if ip, ok := a.(*stdnet.IPNet); ok && ip.IP.To4() != nil && !ip.IP.IsLoopback() {
				l.addresses = append(l.addresses, ip.IP.String())
			}
		}
		links[iface.Name] = l
	}
	return links
}
