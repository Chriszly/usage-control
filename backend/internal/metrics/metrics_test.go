package metrics

import (
	"context"
	"testing"
)

func TestCollectReadsThisMachine(t *testing.T) {
	collector, err := NewCollector(context.Background(), []string{"/"})
	if err != nil {
		t.Fatalf("NewCollector() error = %v", err)
	}
	snapshot, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if snapshot.Time.IsZero() {
		t.Error("Time is not set")
	}
	if snapshot.CPU.Cores < 1 {
		t.Errorf("CPU.Cores = %d, want at least 1", snapshot.CPU.Cores)
	}
	if p := snapshot.CPU.UsagePercent; p < 0 || p > 100 {
		t.Errorf("CPU.UsagePercent = %v, want 0 to 100", p)
	}
	if snapshot.Memory.TotalBytes == 0 {
		t.Error("Memory.TotalBytes is 0")
	}
	if snapshot.Memory.UsedBytes > snapshot.Memory.TotalBytes {
		t.Errorf("Memory.UsedBytes = %d is more than TotalBytes = %d", snapshot.Memory.UsedBytes, snapshot.Memory.TotalBytes)
	}
	if snapshot.Temperatures == nil {
		t.Error("Temperatures is nil, want an empty list when no sensor is available")
	}
	if len(snapshot.Disks) != 1 || snapshot.Disks[0].Path != "/" || snapshot.Disks[0].TotalBytes == 0 {
		t.Errorf("Disks = %+v, want the usage of /", snapshot.Disks)
	}
	if snapshot.Network == nil {
		t.Error("Network is nil, want an empty list when there is no network card")
	}
}

func TestNewCollectorRefusesUnreadableDiskPaths(t *testing.T) {
	for _, path := range []string{"relative/path", "/does/not/exist"} {
		if _, err := NewCollector(context.Background(), []string{path}); err == nil {
			t.Errorf("NewCollector(%q) error = nil, want an error", path)
		}
	}
}
