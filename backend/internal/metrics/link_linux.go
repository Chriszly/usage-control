package metrics

import (
	"path/filepath"
	"strconv"

	"github.com/Chriszly/usage-control/backend/internal/sysfile"
)

// readLinks reads the speed of each interface from /sys/class/net and the
// addresses from the routing files of the host's first process, which
// describe the host's network also inside a container.
func readLinks(names []string) map[string]link {
	netDir := filepath.Join(hostPath("HOST_PROC", "/proc"), "1", "net")
	var addresses map[string][]string
	trie, trieErr := sysfile.Read(filepath.Join(netDir, "fib_trie"))
	routes, routeErr := sysfile.Read(filepath.Join(netDir, "route"))
	if trieErr == nil && routeErr == nil {
		addresses = addressesByInterface(parseLocalAddresses(string(trie)), parseRoutes(string(routes)))
	}

	sysDir := hostPath("HOST_SYS", "/sys")
	links := make(map[string]link, len(names))
	for _, name := range names {
		// Interfaces that are down, and most Wi-Fi cards, report no
		// speed or -1.
		mbps, err := strconv.Atoi(sysfile.Text(filepath.Join(sysDir, "class", "net", name, "speed")))
		if err != nil || mbps < 0 {
			mbps = 0
		}
		links[name] = link{mbps: mbps, addresses: addresses[name]}
	}
	return links
}
