package metrics

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shirou/gopsutil/v4/disk"
)

// readDiskCounters returns the counters of the drive holding each path, such
// as C: for C:\. Windows keeps them per drive letter, and gopsutil only reads
// them for local drives, which do not wait for a server.
func readDiskCounters(ctx context.Context, paths []string, _ map[string]deviceNumber) map[string]ioCounters {
	// gopsutil returns the drives it could read together with an error
	// about the others, so the readings are used even if err is set.
	stats, _ := disk.IOCountersWithContext(ctx)
	result := make(map[string]ioCounters, len(paths))
	for _, path := range paths {
		if s, ok := stats[strings.ToUpper(filepath.VolumeName(path))]; ok {
			result[path] = ioCounters{read: s.ReadBytes, written: s.WriteBytes, operations: s.ReadCount + s.WriteCount}
		}
	}
	return result
}

// diskDevice returns no device: Windows finds the counters by drive letter.
func diskDevice(string) (deviceNumber, bool) {
	return deviceNumber{}, false
}
