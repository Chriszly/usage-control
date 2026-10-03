package metrics

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shirou/gopsutil/v4/disk"
)

// readDiskCounters returns the counters of the drive holding each path, such
// as C: for C:\. Windows keeps them per drive letter.
func readDiskCounters(ctx context.Context, paths []string) map[string]ioCounters {
	// gopsutil returns the drives it could read together with an error
	// about the others, so the readings are used even if err is set.
	stats, _ := disk.IOCountersWithContext(ctx)
	result := make(map[string]ioCounters, len(paths))
	for _, path := range paths {
		if s, ok := stats[strings.ToUpper(filepath.VolumeName(path))]; ok {
			result[path] = ioCounters{read: s.ReadBytes, written: s.WriteBytes}
		}
	}
	return result
}
