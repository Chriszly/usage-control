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

const (
	// diskTimeout is how long the disks may take to answer, all together. A
	// network share whose server is away, or a disk that stopped answering,
	// can keep statfs waiting for good, and the rest of the reading would
	// wait with it.
	diskTimeout = 2 * time.Second
	// diskPause is how long a disk that answered too late is not asked,
	// counted from its late answer, so a share that takes long to answer,
	// even with an error, does not hold up every reading.
	diskPause = 30 * time.Second
)

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

// diskAnswer is what the filesystem behind one path answered: its usage, and
// on Linux the device it is on, which its disk's counters are found by.
type diskAnswer struct {
	path      string
	usage     *disk.UsageStat
	err       error
	device    deviceNumber
	hasDevice bool
}

// diskReader asks the filesystems behind the disk paths for their usage, all
// at the same time, and waits for them at most timeout.
type diskReader struct {
	usage   func(ctx context.Context, path string) (*disk.UsageStat, error)
	device  func(path string) (deviceNumber, bool)
	timeout time.Duration
	pause   time.Duration

	// mu guards asking, the paths whose answer has not come yet; pausedUntil,
	// when the paths that answered too late are asked again; and hanging,
	// those that did not answer in time and have not answered well since,
	// so that is logged once and not at every reading.
	mu          sync.Mutex
	asking      map[string]bool
	pausedUntil map[string]time.Time
	hanging     map[string]bool
}

func newDiskReader() *diskReader {
	return &diskReader{
		usage:       disk.UsageWithContext,
		device:      diskDevice,
		timeout:     diskTimeout,
		pause:       diskPause,
		asking:      map[string]bool{},
		pausedUntil: map[string]time.Time{},
		hanging:     map[string]bool{},
	}
}

// ask asks the filesystem behind each path, each in its own goroutine, and
// returns the answers that came within r.timeout, by path. A path whose
// answer has not come is left to answer in its goroutine, and is not asked
// again until it has and r.pause has passed after that, so a share that
// hangs holds one goroutine and not one more at every reading.
func (r *diskReader) ask(ctx context.Context, paths []string) map[string]diskAnswer {
	answers := make(chan diskAnswer, len(paths))
	var asked []string
	now := time.Now()
	r.mu.Lock()
	for _, path := range paths {
		if r.asking[path] || now.Before(r.pausedUntil[path]) {
			continue
		}
		r.asking[path] = true
		asked = append(asked, path)
	}
	r.mu.Unlock()
	for _, path := range asked {
		go func() { answers <- r.question(ctx, path) }()
	}

	got := make(map[string]diskAnswer, len(asked))
	timeout := time.NewTimer(r.timeout)
	defer timeout.Stop()
	for len(got) < len(asked) {
		select {
		case a := <-answers:
			got[a.path] = a
		case <-ctx.Done():
			return got
		case <-timeout.C:
			for _, path := range asked {
				if _, ok := got[path]; !ok {
					r.notAnswering(path)
				}
			}
			return got
		}
	}
	return got
}

// question asks the filesystem behind path for its usage and its device.
func (r *diskReader) question(ctx context.Context, path string) diskAnswer {
	start := time.Now()
	a := diskAnswer{path: path}
	a.usage, a.err = r.usage(ctx, path)
	if a.err == nil && r.device != nil {
		a.device, a.hasDevice = r.device(path)
	}

	r.mu.Lock()
	delete(r.asking, path)
	if time.Since(start) >= r.timeout {
		r.pausedUntil[path] = time.Now().Add(r.pause)
	} else {
		delete(r.pausedUntil, path)
	}
	again := r.hanging[path] && a.err == nil
	if again {
		delete(r.hanging, path)
	}
	r.mu.Unlock()
	if again {
		slog.Info("the disk answers again", "path", path)
	}
	return a
}

// notAnswering logs that path did not answer in time, once until it answers
// again.
func (r *diskReader) notAnswering(path string) {
	r.mu.Lock()
	first := !r.hanging[path]
	r.hanging[path] = true
	r.mu.Unlock()
	if first {
		slog.Warn("the disk does not answer, so it is left out until it does", "path", path, "timeout", r.timeout)
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
	}
	answers := r.ask(ctx, paths)
	for _, path := range paths {
		if a, ok := answers[path]; ok && a.err != nil {
			return fmt.Errorf("read disk usage of %s: %w", path, a.err)
		}
	}
	return nil
}

// read returns the usage of the filesystem behind each path, and the device
// of each where it is known. A path that cannot be read any more, such as a
// USB disk that was unplugged, or that does not answer in time is left out.
func (r *diskReader) read(ctx context.Context, paths []string) ([]Disk, map[string]deviceNumber) {
	answers := r.ask(ctx, paths)
	disks := make([]Disk, 0, len(paths))
	devices := make(map[string]deviceNumber, len(paths))
	for _, path := range paths {
		a, ok := answers[path]
		if !ok || a.err != nil {
			continue
		}
		disks = append(disks, Disk{
			Path:        path,
			TotalBytes:  a.usage.Total,
			UsedBytes:   a.usage.Used,
			UsedPercent: a.usage.UsedPercent,
		})
		if a.hasDevice {
			devices[path] = a.device
		}
	}
	return disks, devices
}
