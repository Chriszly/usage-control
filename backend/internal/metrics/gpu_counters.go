package metrics

import (
	"regexp"
	"strconv"
)

// This file turns the GPU performance counters of Windows into GPUs. It has no
// build constraint so its tests run on every OS.

// luid is the locally unique ID Windows gives each GPU at start: its high
// part in the upper 32 bits and its low part in the lower ones.
type luid uint64

// adapter is what DirectX tells about a GPU: its name and the size of its own
// memory, 0 for a GPU that shares the main memory.
type adapter struct {
	name        string
	memoryBytes uint64
}

// softwareAdapter is the GPU Windows emulates on the processor when no
// graphics driver is installed. It is no hardware, so it is left out.
const softwareAdapter = "Microsoft Basic Render Driver"

// counterInstance matches the instance names of the "GPU Engine" counters,
// such as pid_1234_luid_0x00000000_0x0000D1A5_phys_0_eng_3_engtype_VideoDecode,
// and of the "GPU Adapter Memory" counters, such as luid_0x00000000_0x0000D1A5_phys_0.
var counterInstance = regexp.MustCompile(`luid_0x([0-9a-fA-F]{1,8})_0x([0-9a-fA-F]{1,8})_phys_([0-9]+)(?:_eng_([0-9]+))?`)

// parseInstance returns the GPU of a counter instance and the engine of the
// GPU it belongs to, which is empty for memory counters.
func parseInstance(name string) (id luid, engine string, ok bool) {
	m := counterInstance.FindStringSubmatch(name)
	if m == nil {
		return 0, "", false
	}
	high, _ := strconv.ParseUint(m[1], 16, 32)
	low, _ := strconv.ParseUint(m[2], 16, 32)
	if m[4] != "" {
		engine = m[3] + "_" + m[4]
	}
	return luid(high<<32 | low), engine, true
}

// gpusFromCounters returns the usage of each GPU the way Task Manager shows
// it: the usage of each engine (3D, copy, video decode, ...) is the sum over
// all processes, and the GPU is as busy as its busiest engine.
// engines and memory are the values of the counters by instance name.
func gpusFromCounters(engines, memory map[string]float64, adapters map[luid]adapter) []GPU {
	engineUsage := map[luid]map[string]float64{}
	for instance, value := range engines {
		id, engine, ok := parseInstance(instance)
		if !ok || engine == "" {
			continue
		}
		if engineUsage[id] == nil {
			engineUsage[id] = map[string]float64{}
		}
		engineUsage[id][engine] += value
	}
	memoryUsed := map[luid]float64{}
	for instance, value := range memory {
		if id, _, ok := parseInstance(instance); ok {
			memoryUsed[id] += value
		}
	}

	gpus := []GPU{}
	for id, usages := range engineUsage {
		info, known := adapters[id]
		if info.name == softwareAdapter {
			continue
		}
		gpu := GPU{Name: "GPU"}
		if known && info.name != "" {
			gpu.Name = info.name
		}
		for _, usage := range usages {
			gpu.UsagePercent = max(gpu.UsagePercent, min(100, usage))
		}
		if info.memoryBytes > 0 {
			gpu.MemoryTotalBytes = info.memoryBytes
			gpu.MemoryUsedBytes = min(info.memoryBytes, uint64(memoryUsed[id]))
		}
		gpus = append(gpus, gpu)
	}
	return sortGPUs(gpus)
}
