package metrics

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
)

// diskTimeout is how long asking the filesystem behind a path may take. A
// network share whose server is away, or a disk that stopped answering, can
// keep statfs waiting for good, and the rest of the reading would wait with
// it.
const diskTimeout = 2 * time.Second

// Disk is the usage of the filesystem that holds one path, and the activity
// of its disk. The activity is left out where it cannot be read; busy and
// latency are only reported by Linux.
type Disk struct {
	Path                string   `json:"path"`
	TotalBytes          uint64   `json:"totalBytes"`
	UsedBytes           uint64   `json:"usedBytes"`
	UsedPercent         float64  `json:"usedPercent"`
	ReadBytesPerSecond  *float64 `json:"readBytesPerSecond,omitempty"`
	WriteBytesPerSecond *float64 `json:"writeBytesPerSecond,omitempty"`
	OperationsPerSecond *float64 `json:"operationsPerSecond,omitempty"`
	BusyPercent         *float64 `json:"busyPercent,omitempty"`
	LatencyMs           *float64 `json:"latencyMs,omitempty"`
}

// diskReader asks the filesystems behind the disk paths for their usage, and
// waits for each at most timeout.
type diskReader struct {
	usage   func(ctx context.Context, path string) (*disk.UsageStat, error)
	timeout time.Duration

	// mu guards asking, the paths that were asked something that has not
	// returned yet, and hanging, those that did not answer in time, so that
	// is logged once and not at every reading.
	mu      sync.Mutex
	asking  map[string]bool
	hanging map[string]bool
}

func newDiskReader() *diskReader {
	return &diskReader{
		usage:   disk.UsageWithContext,
		timeout: diskTimeout,
		asking:  map[string]bool{},
		hanging: map[string]bool{},
	}
}

// ask runs question about the filesystem behind path and reports whether it
// returned within r.timeout. When it did not, it is left to return in its
// own goroutine, and the path is not asked again until it has, so a share
// that hangs holds one goroutine and not one more at every reading. question
// must only write what the caller reads when ask returns true.
func (r *diskReader) ask(ctx context.Context, path string, question func()) bool {
	r.mu.Lock()
	busy := r.asking[path]
	r.asking[path] = true
	r.mu.Unlock()
	if busy {
		return false
	}

	answered := make(chan struct{})
	go func() {
		question()
		r.mu.Lock()
		delete(r.asking, path)
		r.mu.Unlock()
		close(answered)
	}()
	timeout := time.NewTimer(r.timeout)
	defer timeout.Stop()
	select {
	case <-answered:
		r.mu.Lock()
		again := r.hanging[path]
		delete(r.hanging, path)
		r.mu.Unlock()
		if again {
			slog.Info("the disk answers again", "path", path)
		}
		return true
	case <-ctx.Done():
		return false
	case <-timeout.C:
		r.mu.Lock()
		first := !r.hanging[path]
		r.hanging[path] = true
		r.mu.Unlock()
		if first {
			slog.Warn("the disk does not answer, so it is left out until it does", "path", path, "timeout", r.timeout)
		}
		return false
	}
}

// check reports the first path that is not absolute or cannot be read, so a
// mistake in the settings shows up at start instead of as a missing disk on
// the page. A path that does not answer in time, such as a share whose
// server is away, is not a mistake: it is left out until it answers.
func (r *diskReader) check(ctx context.Context, paths []string) error {
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("disk path %q is not absolute", path)
		}
		var err error
		if !r.ask(ctx, path, func() { _, err = r.usage(ctx, path) }) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read disk usage of %s: %w", path, err)
		}
	}
	return nil
}

// read returns the usage of the filesystem behind each path. A path that
// cannot be read any more, such as a USB disk that was unplugged, or that
// does not answer in time is left out.
func (r *diskReader) read(ctx context.Context, paths []string) []Disk {
	disks := make([]Disk, 0, len(paths))
	for _, path := range paths {
		var usage *disk.UsageStat
		var err error
		if !r.ask(ctx, path, func() { usage, err = r.usage(ctx, path) }) || err != nil {
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
