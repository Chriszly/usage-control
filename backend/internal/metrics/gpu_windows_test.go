package metrics

import (
	"context"
	"testing"
)

// TestGPUReaderReadsThisMachine checks the performance counters can be read on
// a real Windows machine without crashing. The CI machine may have no GPU, or
// even no GPU counters, so any list will do.
func TestGPUReaderReadsThisMachine(t *testing.T) {
	reader := newGPUReader()
	if reader.err != nil {
		t.Skipf("this machine has no GPU counters: %v", reader.err)
	}
	reader.read(context.Background())
	gpus := reader.read(context.Background())
	for _, gpu := range gpus {
		if gpu.UsagePercent < 0 || gpu.UsagePercent > 100 {
			t.Errorf("GPU %q usage = %v, want 0 to 100", gpu.Name, gpu.UsagePercent)
		}
	}
	t.Logf("GPUs: %+v, DirectX adapters: %+v", gpus, adapters())
}
