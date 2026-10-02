package metrics

import (
	"context"
	"testing"
)

func TestCollectReadsThisMachine(t *testing.T) {
	snapshot, err := NewCollector().Collect(context.Background())
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
}
