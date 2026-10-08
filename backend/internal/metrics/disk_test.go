package metrics

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
)

// fakeDisks answers statfs for each path at once, except for /mnt/nas, which
// waits until release is closed, as a hard NFS mount whose server is away
// does, and /mnt/gone, which does not exist.
type fakeDisks struct {
	release chan struct{}
	freed   sync.Once
	asked   atomic.Int64
}

// free lets the hanging disk answer.
func (f *fakeDisks) free() { f.freed.Do(func() { close(f.release) }) }

func (f *fakeDisks) usage(_ context.Context, path string) (*disk.UsageStat, error) {
	switch path {
	case "/mnt/nas":
		f.asked.Add(1)
		<-f.release
	case "/mnt/gone":
		return nil, errors.New("no such file or directory")
	}
	return &disk.UsageStat{Path: path, Total: 100, Used: 25, UsedPercent: 25}, nil
}

// newFakeDiskReader returns a diskReader that asks fakeDisks, and lets the
// hanging disk answer when the test ends.
func newFakeDiskReader(t *testing.T) (*diskReader, *fakeDisks) {
	t.Helper()
	fake := &fakeDisks{release: make(chan struct{})}
	t.Cleanup(fake.free)
	r := newDiskReader()
	r.usage, r.timeout = fake.usage, 20*time.Millisecond
	return r, fake
}

func diskPathsOf(disks []Disk) []string {
	var paths []string
	for _, d := range disks {
		paths = append(paths, d.Path)
	}
	return paths
}

func TestDiskReaderLeavesOutAHangingDisk(t *testing.T) {
	r, fake := newFakeDiskReader(t)
	ctx := context.Background()

	for range 3 {
		disks := r.read(ctx, []string{"/", "/mnt/nas", "/mnt/gone"})
		if got := diskPathsOf(disks); len(got) != 1 || got[0] != "/" || disks[0].UsedPercent != 25 {
			t.Fatalf("read() = %v, want only / while /mnt/nas hangs", got)
		}
	}
	// The hanging statfs is asked once, not again at every reading.
	if got := fake.asked.Load(); got != 1 {
		t.Errorf("statfs of the hanging disk asked %d times, want 1", got)
	}

	// Once it answers, it is read again.
	fake.free()
	deadline := time.Now().Add(time.Second)
	for len(diskPathsOf(r.read(ctx, []string{"/", "/mnt/nas"}))) != 2 {
		if time.Now().After(deadline) {
			t.Fatal("read() still leaves out /mnt/nas after it answers")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestDiskReaderStopsWaitingWithTheReading(t *testing.T) {
	r, _ := newFakeDiskReader(t)
	r.timeout = time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if got := diskPathsOf(r.read(ctx, []string{"/mnt/nas"})); len(got) != 0 {
		t.Errorf("read() = %v, want nothing", got)
	}
}

func TestDiskReaderCheck(t *testing.T) {
	r, _ := newFakeDiskReader(t)
	ctx := context.Background()
	// A disk that does not answer does not keep the program from starting.
	if err := r.check(ctx, []string{"/", "/mnt/nas"}); err != nil {
		t.Errorf("check() with a hanging disk error = %v, want nil", err)
	}
	if err := r.check(ctx, []string{"/mnt/gone"}); err == nil {
		t.Error("check() of a missing path error = nil, want an error")
	}
	if err := r.check(ctx, []string{"mnt"}); err == nil {
		t.Error("check() of a relative path error = nil, want an error")
	}
}
