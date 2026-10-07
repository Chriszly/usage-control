package metrics

import (
	"context"
	"path/filepath"
	"sync"

	"github.com/Chriszly/usage-control/backend/internal/sysfile"
	"golang.org/x/sys/unix"
)

// readDiskCounters returns the counters of the disk or partition holding each
// path, from /proc/diskstats. A path on a filesystem without a disk of its own,
// such as a network share, has none.
func readDiskCounters(_ context.Context, paths []string) map[string]ioCounters {
	text, err := sysfile.Read(filepath.Join(hostPath("HOST_PROC", "/proc"), "diskstats"))
	if err != nil {
		return nil
	}
	stats := parseDiskstats(string(text))
	result := make(map[string]ioCounters, len(paths))
	for _, path := range paths {
		var stat unix.Stat_t
		if err := unix.Stat(path, &stat); err != nil {
			continue
		}
		device := deviceNumber{major: unix.Major(stat.Dev), minor: unix.Minor(stat.Dev)}
		counters, ok := stats[device]
		if !ok && path == "/" {
			// Inside a container / is an overlay with no disk of its own;
			// its files are on the disk of the host's /.
			if root, found := hostRootDevice(); found {
				counters, ok = stats[root]
			}
		}
		if ok {
			result[path] = counters
		}
	}
	return result
}

// hostRootDevice returns the device of the host's root filesystem, read once
// from the mounts of the host's first process.
var hostRootDevice = sync.OnceValues(func() (deviceNumber, bool) {
	text, err := sysfile.Read(filepath.Join(hostPath("HOST_PROC", "/proc"), "1", "mountinfo"))
	if err != nil {
		return deviceNumber{}, false
	}
	return parseMountinfoRoot(string(text))
})
