package metrics

import (
	"context"
	"log/slog"
	stdnet "net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
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
	// Errors and Dropped count the packets that arrived or left broken, and
	// those the interface threw away, since the machine booted.
	Errors  uint64 `json:"errors,omitempty"`
	Dropped uint64 `json:"dropped,omitempty"`
	// LinkMbps is the speed the interface is connected at, in Mbit/s, and
	// Addresses its IPv4 addresses; both are left out where unknown.
	LinkMbps  int      `json:"linkMbps,omitempty"`
	Addresses []string `json:"addresses,omitempty"`
}

// counters is the number of bytes an interface has received and sent since
// the machine booted, and of packets with errors and dropped ones.
type counters struct {
	received uint64
	sent     uint64
	errors   uint64
	dropped  uint64
}

// readNetworkCounters returns the byte counters of the machine's physical
// network interfaces.
func readNetworkCounters(ctx context.Context) (map[string]counters, error) {
	stats, err := readInterfaceStats(ctx)
	if err != nil {
		return nil, err
	}
	isVirtual := virtualInterfaceCheck()
	result := make(map[string]counters, len(stats))
	for _, s := range stats {
		if isVirtual(s.Name) {
			continue
		}
		result[s.Name] = counters{
			received: s.BytesRecv,
			sent:     s.BytesSent,
			errors:   s.Errin + s.Errout,
			dropped:  s.Dropin + s.Dropout,
		}
	}
	return result, nil
}

// readInterfaceStats reads the counters of every network interface. On Linux
// it reads them from the first process of the host's /proc: /proc/net
// describes the network of the reading process, which inside a container is
// the container's own network, not the host's. Where the first process is
// hidden, as with hidepid, it reads /proc/net instead, which outside a
// container is the same network. Inside one, that would be the container's
// network, so there the error is returned. A container is told by HOST_PROC
// being set, as for the hostname: when the first process cannot be read, its
// network namespace cannot be compared with this program's either, so a
// container started without HOST_PROC would read its own network here.
func readInterfaceStats(ctx context.Context) ([]net.IOCountersStat, error) {
	if runtime.GOOS == "linux" {
		return readLinuxInterfaceStats(ctx, hostPath("HOST_PROC", "/proc"), os.Getenv("HOST_PROC") != "")
	}
	return net.IOCountersWithContext(ctx, true)
}

// readLinuxInterfaceStats reads the counters from procDir/1/net/dev, or
// outside a container, when that fails, from procDir/net/dev.
func readLinuxInterfaceStats(ctx context.Context, procDir string, inContainer bool) ([]net.IOCountersStat, error) {
	stats, err := net.IOCountersByFileWithContext(ctx, true, filepath.Join(procDir, "1", "net", "dev"))
	if err == nil || inContainer {
		return stats, err
	}
	ownNetworkOnce.Do(func() {
		slog.Warn("cannot read the network of the first process; reading this program's own network instead", "error", err)
	})
	return net.IOCountersByFileWithContext(ctx, true, filepath.Join(procDir, "net", "dev"))
}

// ownNetworkOnce logs only once that the network is read from /proc/net.
var ownNetworkOnce sync.Once

// virtualInterfaceCheck returns a function that reports whether an interface
// is not a network card. Linux tells from /sys; other systems only from the
// loopback flag, which also hides Windows' "Loopback Pseudo-Interface 1".
func virtualInterfaceCheck() func(name string) bool {
	if runtime.GOOS == "linux" {
		sysDir := hostPath("HOST_SYS", "/sys")
		return func(name string) bool { return isVirtualInterface(sysDir, name) }
	}
	loopbacks := loopbacks.get(time.Now())
	return func(name string) bool { return loopbacks[name] }
}

// loopbackList lists the loopback interfaces at most once a minute: listing
// the interfaces is a system call per reading otherwise, and loopback
// interfaces hardly ever change.
type loopbackList struct {
	read func() map[string]bool

	mu     sync.Mutex
	names  map[string]bool
	readAt time.Time
}

var loopbacks = &loopbackList{read: readLoopbackInterfaces}

// get returns the loopback interfaces, listed again when the list is a
// minute old or could not be read last time.
func (l *loopbackList) get(now time.Time) map[string]bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.names == nil || now.Sub(l.readAt) >= time.Minute {
		l.names, l.readAt = l.read(), now
	}
	return l.names
}

// readLoopbackInterfaces returns the names of the interfaces the OS flags as
// loopback. If the OS cannot list them, none are left out.
func readLoopbackInterfaces() map[string]bool {
	interfaces, err := stdnet.Interfaces()
	if err != nil {
		return nil
	}
	loopbacks := make(map[string]bool)
	for _, iface := range interfaces {
		if iface.Flags&stdnet.FlagLoopback != 0 {
			loopbacks[iface.Name] = true
		}
	}
	return loopbacks
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
		iface := NetworkInterface{
			Name:          name,
			ReceivedBytes: now.received,
			SentBytes:     now.sent,
			Errors:        now.errors,
			Dropped:       now.dropped,
		}
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
