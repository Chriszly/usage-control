package metrics

import (
	"slices"
	"strings"
)

// GPU is the usage of one graphics processor. Memory and temperature are
// left out where the GPU or the OS does not report them, such as the memory of
// a GPU that shares the main memory.
type GPU struct {
	Name             string   `json:"name"`
	UsagePercent     float64  `json:"usagePercent"`
	MemoryTotalBytes uint64   `json:"memoryTotalBytes,omitempty"`
	MemoryUsedBytes  uint64   `json:"memoryUsedBytes,omitempty"`
	Celsius          *float64 `json:"celsius,omitempty"`
}

// MemoryUsedPercent returns how much of the GPU's own memory is used, and
// false when the GPU reports no memory of its own.
func (g GPU) MemoryUsedPercent() (float64, bool) {
	if g.MemoryTotalBytes == 0 {
		return 0, false
	}
	return min(100, float64(g.MemoryUsedBytes)/float64(g.MemoryTotalBytes)*100), true
}

// NvidiaGPUKey returns what names an NVIDIA GPU in the ids of the add-ons'
// extras, from what nvidia-smi reports as its index and uuid, such as
// "GPU-1a2b3c4d-…", when it reports gpus GPUs. The index can change at boot,
// so with several GPUs it is the first 8 digits of the UUID, which stays
// with the card. A machine's only GPU, at index 0, keeps the index, so its
// history goes on from before the UUID was used.
func NvidiaGPUKey(index, uuid string, gpus int) string {
	short := strings.ToLower(strings.TrimPrefix(uuid, "GPU-"))
	if (gpus == 1 && index == "0") || len(short) < 8 || strings.ContainsAny(short[:8], "[ ") {
		return index
	}
	return short[:8]
}

// IsNvidiaLostGPU reports whether a line nvidia-smi printed stands for a GPU
// it cannot reach at all, such as one that fell off the bus, which gets
// this message instead of a row: "Unable to determine the device handle for
// GPU0000:02:00.0: GPU is lost. …". Such a GPU is counted for NvidiaGPUKey
// like a row, so the others keep their key while it fails.
func IsNvidiaLostGPU(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "Unable to determine the device handle")
}

// sortGPUs sorts GPUs by name and numbers GPUs with the same name, such as two
// identical graphics cards, so each name stands for one GPU in the history.
func sortGPUs(gpus []GPU) []GPU {
	slices.SortStableFunc(gpus, func(a, b GPU) int { return strings.Compare(a.Name, b.Name) })
	names := make([]string, len(gpus))
	for i, g := range gpus {
		names[i] = g.Name
	}
	for i, name := range NumberDuplicates(names) {
		gpus[i].Name = name
	}
	return gpus
}
