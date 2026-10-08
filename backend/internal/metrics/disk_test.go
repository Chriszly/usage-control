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

// fakeDisks answers statfs for each path at once, except for /mnt/nas,
// /mnt/nas2 and /mnt/nas3, which wait until release is closed, as a hard NFS mount whose
// server is away does, and /mnt/gone, which does not exist.
type fakeDisks struct {
	release chan struct{}
	freed   sync.Once
	// lateErr is what the waiting paths answer once released.
	lateErr error
	asked   atomic.Int64
}

// free lets the waiting disks answer.
func (f *fakeDisks) free() { f.freed.Do(func() { close(f.release) }) }

func (f *fakeDisks) usage(_ context.Context, path string) (*disk.UsageStat, error) {
	switch path {
	case "/mnt/nas", "/mnt/nas2", "/mnt/nas3":
		f.asked.Add(1)
		<-f.release
		if f.lateErr != nil {
			return nil, f.lateErr
		}
	case "/mnt/gone":
		return nil, errors.New("no such file or directory")
	}
	return &disk.UsageStat{Path: path, Total: 100, Used: 25, UsedPercent: 25}, nil
}

// newFakeDiskReader returns a diskReader that asks fakeDisks, and lets the
// waiting disks answer when the test ends.
func newFakeDiskReader(t *testing.T) (*diskReader, *fakeDisks) {
	t.Helper()
	fake := &fakeDisks{release: make(chan struct{})}
	t.Cleanup(fake.free)
	r := newDiskReader()
	r.usage, r.device, r.timeout, r.pause = fake.usage, nil, 20*time.Millisecond, 0
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
		disks, _ := r.read(ctx, []string{"/", "/mnt/nas", "/mnt/gone"})
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
	for {
		disks, _ := r.read(ctx, []string{"/", "/mnt/nas"})
		if len(disks) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("read() still leaves out /mnt/nas after it answers")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestDiskReaderWaitsForTheDisksTogether(t *testing.T) {
	r, _ := newFakeDiskReader(t)
	r.timeout = 200 * time.Millisecond
	start := time.Now()
	disks, _ := r.read(context.Background(), []string{"/mnt/nas", "/", "/mnt/nas2", "/mnt/nas3"})
	// One after another, the three hanging disks would take three times
	// the limit.
	if waited := time.Since(start); waited >= r.timeout*5/2 {
		t.Errorf("read() of three hanging disks took %v, want about %v", waited, r.timeout)
	}
	if got := diskPathsOf(disks); len(got) != 1 || got[0] != "/" {
		t.Errorf("read() = %v, want only /", got)
	}
}

func TestDiskReaderPausesADiskThatAnsweredLate(t *testing.T) {
	for _, lateErr := range []error{errors.New("host is down"), nil} {
		r, fake := newFakeDiskReader(t)
		r.pause = time.Hour
		fake.lateErr = lateErr
		ctx := context.Background()

		r.read(ctx, []string{"/mnt/nas"})
		fake.free()
		// Wait for the late answer to arrive.
		deadline := time.Now().Add(time.Second)
		for {
			r.mu.Lock()
			paused := !r.pausedUntil["/mnt/nas"].IsZero()
			hanging := r.hanging["/mnt/nas"]
			r.mu.Unlock()
			if paused {
				// A late answer, good or not, is no sign that the disk
				// answers again.
				if !hanging {
					t.Errorf("a late answer (error %v) marked the disk as answering again", lateErr)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("a late answer (error %v) did not pause the disk", lateErr)
			}
			time.Sleep(time.Millisecond)
		}
		if disks, _ := r.read(ctx, []string{"/mnt/nas"}); len(disks) != 0 || fake.asked.Load() != 1 {
			t.Errorf("read() of the paused disk = %v after asking %d times, want nothing after 1", diskPathsOf(disks), fake.asked.Load())
		}
	}
}

func TestDiskReaderStopsWaitingWithTheReading(t *testing.T) {
	r, _ := newFakeDiskReader(t)
	r.timeout = time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if disks, _ := r.read(ctx, []string{"/mnt/nas"}); len(disks) != 0 {
		t.Errorf("read() = %v, want nothing", diskPathsOf(disks))
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
