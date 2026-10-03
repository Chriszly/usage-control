package metrics

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/shirou/gopsutil/v4/disk"
)

// Disk is the usage of the filesystem that holds one path, and how fast its
// disk reads and writes. The speeds are left out where they cannot be read.
type Disk struct {
	Path                string   `json:"path"`
	TotalBytes          uint64   `json:"totalBytes"`
	UsedBytes           uint64   `json:"usedBytes"`
	UsedPercent         float64  `json:"usedPercent"`
	ReadBytesPerSecond  *float64 `json:"readBytesPerSecond,omitempty"`
	WriteBytesPerSecond *float64 `json:"writeBytesPerSecond,omitempty"`
}

// checkDiskPaths reports the first path that is not absolute or cannot be
// read, so a mistake in the settings shows up at start instead of as a
// missing disk on the page.
func checkDiskPaths(ctx context.Context, paths []string) error {
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("disk path %q is not absolute", path)
		}
		if _, err := disk.UsageWithContext(ctx, path); err != nil {
			return fmt.Errorf("read disk usage of %s: %w", path, err)
		}
	}
	return nil
}

// readDisks returns the usage of the filesystem behind each path. A path that
// cannot be read any more, such as a USB disk that was unplugged, is left out.
func readDisks(ctx context.Context, paths []string) []Disk {
	disks := make([]Disk, 0, len(paths))
	for _, path := range paths {
		usage, err := disk.UsageWithContext(ctx, path)
		if err != nil {
			continue
		}
		disks = append(disks, Disk{
			Path:        path,
			TotalBytes:  usage.Total,
			UsedBytes:   usage.Used,
			UsedPercent: usage.UsedPercent,
		})
	}
	return disks
}
