package metrics

import (
	"fmt"
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
	count := make(map[string]int, len(gpus))
	for _, g := range gpus {
		count[g.Name]++
	}
	seen := make(map[string]int, len(gpus))
	for i, g := range gpus {
		if count[g.Name] > 1 {
			seen[g.Name]++
			gpus[i].Name = fmt.Sprintf("%s %d", g.Name, seen[g.Name])
		}
	}
	return gpus
}
