package metrics

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/net"
)

// NetworkInterface is the traffic of one network interface.
type NetworkInterface struct {
	Name                  string  `json:"name"`
	ReceivedBytes         uint64  `json:"receivedBytes"`
	SentBytes             uint64  `json:"sentBytes"`
	ReceiveBytesPerSecond float64 `json:"receiveBytesPerSecond"`
	SendBytesPerSecond    float64 `json:"sendBytesPerSecond"`
}

// counters is the number of bytes an interface has received and sent since
// the machine booted.
type counters struct {
	received uint64
	sent     uint64
}

// readNetworkCounters returns the byte counters of the machine's physical
// network interfaces.
func readNetworkCounters(ctx context.Context) (map[string]counters, error) {
	stats, err := readInterfaceStats(ctx)
	if err != nil {
		return nil, err
	}
	sysDir := hostPath("HOST_SYS", "/sys")
	result := make(map[string]counters, len(stats))
	for _, s := range stats {
		if isVirtualInterface(sysDir, s.Name) {
			continue
		}
		result[s.Name] = counters{received: s.BytesRecv, sent: s.BytesSent}
	}
	return result, nil
}

// readInterfaceStats reads the counters of every network interface. On Linux
// it reads them from the first process of the host's /proc: /proc/net
// describes the network of the reading process, which inside a container is
// the container's own network, not the host's.
func readInterfaceStats(ctx context.Context) ([]net.IOCountersStat, error) {
	if runtime.GOOS == "linux" {
		file := filepath.Join(hostPath("HOST_PROC", "/proc"), "1", "net", "dev")
		return net.IOCountersByFileWithContext(ctx, true, file)
	}
	return net.IOCountersWithContext(ctx, true)
}

// isVirtualInterface reports whether an interface is not a network card:
// loopback, Docker bridges and the like. Linux lists those under
// /sys/devices/virtual/net. Where /sys does not exist, only the loopback
// interface is treated as virtual.
func isVirtualInterface(sysDir, name string) bool {
	target, err := os.Readlink(filepath.Join(sysDir, "class", "net", name))
	if err != nil {
		return name == "lo"
	}
	return strings.Contains(target, "/virtual/")
}

// throughput turns two readings of the counters into bytes per second. The
// first reading, and an interface whose counters went down because it was
// reset, have a speed of 0.
func throughput(previous, current map[string]counters, elapsed time.Duration) []NetworkInterface {
	interfaces := make([]NetworkInterface, 0, len(current))
	for name, now := range current {
		iface := NetworkInterface{Name: name, ReceivedBytes: now.received, SentBytes: now.sent}
		if before, ok := previous[name]; ok && elapsed > 0 {
			iface.ReceiveBytesPerSecond = perSecond(before.received, now.received, elapsed)
			iface.SendBytesPerSecond = perSecond(before.sent, now.sent, elapsed)
		}
		interfaces = append(interfaces, iface)
	}
	slices.SortFunc(interfaces, func(a, b NetworkInterface) int { return strings.Compare(a.Name, b.Name) })
	return interfaces
}

func perSecond(before, now uint64, elapsed time.Duration) float64 {
	if now < before {
		return 0
	}
	return float64(now-before) / elapsed.Seconds()
}

// hostPath returns the value of the environment variable that says where the
// host's /proc or /sys is mounted, or fallback when it is not set.
func hostPath(variable, fallback string) string {
	if path := os.Getenv(variable); path != "" {
		return path
	}
	return fallback
}
