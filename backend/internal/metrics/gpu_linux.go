package metrics

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
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
	sysDir    string
	nvidiaSMI string

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
	// Not installed, which is the usual case, means no NVIDIA GPU is read.
	nvidiaSMI, _ := exec.LookPath("nvidia-smi")
	return &gpuReader{
		sysDir:    hostPath("HOST_SYS", "/sys"),
		nvidiaSMI: nvidiaSMI,
		v3d:       map[string]v3dReading{},
	}
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
	if r.nvidiaSMI != "" {
		gpus = append(gpus, readNvidiaSMI(ctx, r.nvidiaSMI)...)
	}
	return sortGPUs(gpus)
}

// readCard reads one DRM card, and reports false for a card whose usage the
// kernel does not report, such as a display controller.
func (r *gpuReader) readCard(card string) (GPU, bool) {
	device := filepath.Join(card, "device")
	if busy, err := readUint(filepath.Join(device, "gpu_busy_percent")); err == nil {
		return readAMDGPU(device, busy), true
	}
	stats, err := readFile(filepath.Join(device, "gpu_stats"))
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

// readAMDGPU reads the memory and temperature of an AMD GPU that is busy
// percent of the time.
func readAMDGPU(device string, busy uint64) GPU {
	gpu := GPU{Name: "AMD GPU", UsagePercent: min(100, float64(busy))}
	if name, err := readFile(filepath.Join(device, "product_name")); err == nil && strings.TrimSpace(string(name)) != "" {
		gpu.Name = strings.TrimSpace(string(name))
	}
	total, totalErr := readUint(filepath.Join(device, "mem_info_vram_total"))
	used, usedErr := readUint(filepath.Join(device, "mem_info_vram_used"))
	if totalErr == nil && usedErr == nil {
		gpu.MemoryTotalBytes, gpu.MemoryUsedBytes = total, used
	}
	sensors, _ := filepath.Glob(filepath.Join(device, "hwmon", "hwmon*", "temp1_input"))
	if len(sensors) > 0 {
		if milli, err := readUint(sensors[0]); err == nil {
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

// readNvidiaSMI asks nvidia-smi for the usage of the NVIDIA GPUs. It gives up
// after a second, so a hanging driver does not hold up the other values.
func readNvidiaSMI(ctx context.Context, program string) []GPU {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	query := "--query-gpu=name,utilization.gpu,memory.used,memory.total,temperature.gpu"
	// program is the nvidia-smi found on the PATH at start, and the arguments are fixed.
	out, err := exec.CommandContext(ctx, program, query, "--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil
	}
	return parseNvidiaSMI(string(out))
}

// parseNvidiaSMI reads the CSV nvidia-smi writes for readNvidiaSMI's query,
// one line per GPU. A value the GPU does not report, written as [N/A] or
// [Not Supported], is left out; a GPU without a usage is left out completely.
func parseNvidiaSMI(out string) []GPU {
	gpus := []GPU{}
	for line := range strings.Lines(out) {
		fields := strings.Split(line, ",")
		if len(fields) < 5 {
			continue
		}
		// The name comes first and is the only field that could hold a comma.
		values := fields[len(fields)-4:]
		number := func(i int) (float64, bool) {
			v, err := strconv.ParseFloat(strings.TrimSpace(values[i]), 64)
			return v, err == nil
		}
		usage, ok := number(0)
		if !ok {
			continue
		}
		gpu := GPU{
			Name:         strings.TrimSpace(strings.Join(fields[:len(fields)-4], ",")),
			UsagePercent: min(100, usage),
		}
		const mebibyte = 1 << 20
		used, usedOK := number(1)
		total, totalOK := number(2)
		if usedOK && totalOK {
			gpu.MemoryUsedBytes, gpu.MemoryTotalBytes = uint64(used*mebibyte), uint64(total*mebibyte)
		}
		if celsius, ok := number(3); ok {
			gpu.Celsius = &celsius
		}
		gpus = append(gpus, gpu)
	}
	return gpus
}

// readUint reads a file that holds one whole number, as most files in /sys do.
func readUint(path string) (uint64, error) {
	text, err := readFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(text)), 10, 64)
}

// readFile reads a file under /sys. Its path is made of the names the kernel
// gives its files, with no input from a user in it.
func readFile(path string) ([]byte, error) {
	return os.ReadFile(path) //nolint:gosec // see above
}
