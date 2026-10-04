//go:build linux || windows

package metrics

import (
	"context"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// nvidiaSMIInterval is how long an nvidia-smi answer is used again. Starting
// a process for every reading costs more than the values are worth, and the
// page's 2 second refresh does not need every one of them.
const nvidiaSMIInterval = 4 * time.Second

// nvidiaSMI reads the NVIDIA GPUs through nvidia-smi, which comes with the
// NVIDIA driver, asking it at most every nvidiaSMIInterval.
type nvidiaSMI struct {
	// program is where nvidia-smi is; empty when it is not installed.
	program string

	// mu guards the last answer, gpus, and when it came.
	mu   sync.Mutex
	gpus []GPU
	at   time.Time
}

func newNvidiaSMI() *nvidiaSMI {
	// Not installed, which is the usual case without an NVIDIA GPU, means no
	// NVIDIA GPU is read.
	program, _ := exec.LookPath("nvidia-smi")
	return &nvidiaSMI{program: program}
}

// read returns the NVIDIA GPUs, asking nvidia-smi again once its last answer
// is nvidiaSMIInterval old. A nil nvidiaSMI reads none.
func (n *nvidiaSMI) read(ctx context.Context) []GPU {
	if n == nil || n.program == "" {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if now := time.Now(); now.Sub(n.at) >= nvidiaSMIInterval {
		n.gpus, n.at = readNvidiaSMI(ctx, n.program), now
	}
	return n.gpus
}

// temperatures returns the temperature of each NVIDIA GPU that reports one,
// named after the GPU and numbered like the GPUs, so two identical cards get
// two names.
func (n *nvidiaSMI) temperatures(ctx context.Context) []Temperature {
	var temperatures []Temperature
	for _, gpu := range sortGPUs(slices.Clone(n.read(ctx))) {
		if gpu.Celsius != nil {
			temperatures = append(temperatures, Temperature{Sensor: gpu.Name, Celsius: *gpu.Celsius})
		}
	}
	return temperatures
}

// readNvidiaSMI asks nvidia-smi for the usage of the NVIDIA GPUs. It gives up
// after a second, so a hanging driver does not hold up the other values.
func readNvidiaSMI(ctx context.Context, program string) []GPU {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	query := "--query-gpu=name,utilization.gpu,memory.used,memory.total,temperature.gpu"
	// program is the nvidia-smi found on the PATH at start, and the arguments are fixed.
	cmd := exec.CommandContext(ctx, program, query, "--format=csv,noheader,nounits")
	hideWindow(cmd)
	out, err := cmd.Output()
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
