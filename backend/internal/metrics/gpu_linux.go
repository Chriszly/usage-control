package metrics

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/Chriszly/usage-control/backend/internal/sysfile"
)

// gpuReader reads the GPUs Linux reports usage for without special rights:
//
//   - AMD GPUs (amdgpu driver) through /sys/class/drm/card*/device/gpu_busy_percent
//   - the Raspberry Pi's VideoCore GPU (v3d driver) through .../device/gpu_stats
//   - NVIDIA GPUs through nvidia-smi, when it is installed
//
// Intel GPUs report their usage only to programs with extra rights, so they
// are left out.
type gpuReader struct {
	sysDir string
	nvidia *nvidiaSMI

	// mu guards the previous gpu_stats reading of each v3d GPU, which its
	// usage is measured against.
	mu  sync.Mutex
	v3d map[string]v3dReading
}

// v3dReading is one reading of a v3d GPU's gpu_stats: the clock and how long
// each of its queues has been busy, all in nanoseconds.
type v3dReading struct {
	clock uint64
	busy  map[string]uint64
}

func newGPUReader() *gpuReader {
	r := &gpuReader{
		sysDir: hostPath("HOST_SYS", "/sys"),
		nvidia: newNvidiaSMI(),
		v3d:    map[string]v3dReading{},
	}
	r.nvidia.sleep = NewNvidiaSleep(r.sysDir)
	return r
}

var cardName = regexp.MustCompile(`^card[0-9]+$`)

func (r *gpuReader) read(ctx context.Context) []GPU {
	gpus := []GPU{}
	cards, _ := filepath.Glob(filepath.Join(r.sysDir, "class", "drm", "card*"))
	for _, card := range cards {
		if !cardName.MatchString(filepath.Base(card)) {
			continue // a display connector such as card0-HDMI-A-1
		}
		if gpu, ok := r.readCard(card); ok {
			gpus = append(gpus, gpu)
		}
	}
	gpus = append(gpus, r.nvidia.read(ctx)...)
	return sortGPUs(gpus)
}

// temperatures returns no GPU temperature: the GPU card shows those of NVIDIA
// GPUs, and the kernel lists those of AMD GPUs with the other sensors.
func (*gpuReader) temperatures(context.Context) []Temperature { return nil }

// readCard reads one DRM card, and reports false for a card whose usage the
// kernel does not report, such as a display controller. An AMD GPU the
// kernel has put to sleep is reported idle without asking it for its usage
// and temperature, which would wake it.
func (r *gpuReader) readCard(card string) (GPU, bool) {
	device := filepath.Join(card, "device")
	busyFile := filepath.Join(device, "gpu_busy_percent")
	if isSuspended(device) {
		if _, err := os.Stat(busyFile); err == nil {
			return readAMDGPU(device, 0, false), true
		}
	}
	if busy, ok := sysfile.Uint(busyFile); ok {
		return readAMDGPU(device, busy, true), true
	}
	stats, err := sysfile.Read(filepath.Join(device, "gpu_stats"))
	if err != nil {
		return GPU{}, false
	}
	current, err := parseV3DStats(string(stats))
	if err != nil {
		return GPU{}, false
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	usage := v3dUsage(r.v3d[card], current)
	r.v3d[card] = current
	return GPU{Name: "VideoCore GPU", UsagePercent: usage}, true
}

// readAMDGPU reads the memory and, when it is awake, the temperature of an
// AMD GPU that is busy percent of the time. Its name and memory are what the
// driver keeps, so reading them does not wake it.
func readAMDGPU(device string, busy uint64, awake bool) GPU {
	gpu := GPU{Name: "AMD GPU", UsagePercent: min(100, float64(busy))}
	if name, err := sysfile.Read(filepath.Join(device, "product_name")); err == nil && strings.TrimSpace(string(name)) != "" {
		gpu.Name = strings.TrimSpace(string(name))
	}
	total, totalOK := sysfile.Uint(filepath.Join(device, "mem_info_vram_total"))
	used, usedOK := sysfile.Uint(filepath.Join(device, "mem_info_vram_used"))
	if totalOK && usedOK {
		gpu.MemoryTotalBytes, gpu.MemoryUsedBytes = total, used
	}
	if !awake {
		return gpu
	}
	sensors, _ := filepath.Glob(filepath.Join(device, "hwmon", "hwmon*", "temp1_input"))
	if len(sensors) > 0 {
		if milli, ok := sysfile.Uint(sensors[0]); ok {
			celsius := float64(milli) / 1000
			gpu.Celsius = &celsius
		}
	}
	return gpu
}

// parseV3DStats reads the gpu_stats file of the v3d driver: a header line,
// then one line per queue with its name, the clock, the number of jobs and how
// long the queue has been busy.
func parseV3DStats(text string) (v3dReading, error) {
	reading := v3dReading{busy: map[string]uint64{}}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) != 4 {
			return v3dReading{}, errors.New("unexpected gpu_stats line: " + line)
		}
		clock, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return v3dReading{}, err
		}
		busy, err := strconv.ParseUint(fields[3], 10, 64)
		if err != nil {
			return v3dReading{}, err
		}
		reading.clock = clock
		reading.busy[fields[0]] = busy
	}
	if len(reading.busy) == 0 {
		return v3dReading{}, errors.New("gpu_stats lists no queue")
	}
	return reading, nil
}

// v3dUsage returns the usage of a v3d GPU between two readings: the share of
// time its busiest queue was busy, as the GPU runs its queues side by side.
func v3dUsage(previous, current v3dReading) float64 {
	usage := 0.0
	for queue, busy := range current.busy {
		before, ok := previous.busy[queue]
		if !ok {
			continue
		}
		usage = max(usage, busyShare(before, busy, previous.clock, current.clock))
	}
	return usage
}

// busyShare returns how much of the time between two readings something was
// busy, in percent. Counters that went back give 0.
func busyShare(busyBefore, busyNow, clockBefore, clockNow uint64) float64 {
	if clockNow <= clockBefore || busyNow < busyBefore {
		return 0
	}
	return min(100, float64(busyNow-busyBefore)/float64(clockNow-clockBefore)*100)
}
