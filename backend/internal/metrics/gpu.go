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
