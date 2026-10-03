package metrics

import (
	"context"
	"runtime"

	"github.com/shirou/gopsutil/v4/mem"
)

// Swap is the usage of the swap space: swap partitions and files on Linux and
// macOS, the page files on Windows.
type Swap struct {
	TotalBytes  uint64  `json:"totalBytes"`
	UsedBytes   uint64  `json:"usedBytes"`
	UsedPercent float64 `json:"usedPercent"`
}

// readSwap returns the usage of the swap space, or nil when the machine has
// none, as many Raspberry Pis set up without a swap file.
func readSwap(ctx context.Context, memory *mem.VirtualMemoryStat) *Swap {
	var total, used uint64
	switch runtime.GOOS {
	case "linux":
		// Linux reports swap in the same file as the memory, which is
		// already read.
		total, used = memory.SwapTotal, memory.SwapTotal-min(memory.SwapFree, memory.SwapTotal)
	case "windows":
		// Asking Windows for its page files is one call; gopsutil's
		// SwapMemory sets up a performance counter on every call.
		files, err := mem.SwapDevicesWithContext(ctx)
		if err != nil {
			return nil
		}
		for _, f := range files {
			total += f.UsedBytes + f.FreeBytes
			used += f.UsedBytes
		}
	default:
		swap, err := mem.SwapMemoryWithContext(ctx)
		if err != nil {
			return nil
		}
		total, used = swap.Total, swap.Used
	}
	if total == 0 {
		return nil
	}
	return &Swap{TotalBytes: total, UsedBytes: used, UsedPercent: float64(used) / float64(total) * 100}
}
