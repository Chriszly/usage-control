//go:build linux || windows

package metrics

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// nvidiaSMIInterval is how long an nvidia-smi answer is used again.
	// Starting a process for every reading costs more than the values are
	// worth, and the page's 2 second refresh does not need every one of them.
	nvidiaSMIInterval = 10 * time.Second
	// nvidiaSMITimeout is how long nvidia-smi may take. Without the driver's
	// persistence mode, as on many headless Linux servers, each call starts
	// the driver, which can take a few seconds.
	nvidiaSMITimeout = 5 * time.Second
	// nvidiaSMIWait is how long a reading waits for nvidia-smi before it
	// takes the last answer, so a slow driver does not hold up the other
	// values; nvidia-smi goes on in the background and the next reading uses
	// its answer.
	nvidiaSMIWait = time.Second
	// nvidiaSMIStuck is how long past nvidiaSMITimeout a call of nvidia-smi
	// counts as stuck: a driver that fell off the bus can leave it in a
	// state that not even a kill ends. Then no GPU is reported rather than
	// the last answer, which would show a frozen value for good.
	nvidiaSMIStuck = 5 * time.Second
)

// nvidiaSMI reads the NVIDIA GPUs through nvidia-smi, which comes with the
// NVIDIA driver, asking it at most every nvidiaSMIInterval.
type nvidiaSMI struct {
	// program is where nvidia-smi is; empty when it is not installed.
	program string
	// query asks nvidia-smi; nil runs readNvidiaSMI.
	query func(ctx context.Context, program string) []GPU
	// sleep tells whether the GPUs sleep, when nvidia-smi would wake them;
	// nil, as on Windows, never.
	sleep *NvidiaSleep

	// mu guards the last answer, gpus, when it came, and the call under way.
	mu   sync.Mutex
	gpus []GPU
	at   time.Time
	// running is closed when the call of nvidia-smi under way ends; nil
	// while none is. started is when that call started, and stuck whether
	// it was logged as stuck.
	running chan struct{}
	started time.Time
	stuck   bool
}

func newNvidiaSMI() *nvidiaSMI {
	// Not installed, which is the usual case without an NVIDIA GPU, means no
	// NVIDIA GPU is read.
	program, _ := exec.LookPath("nvidia-smi")
	return &nvidiaSMI{program: program}
}

// read returns the NVIDIA GPUs. Once the last answer is nvidiaSMIInterval
// old, it asks nvidia-smi again in the background and waits up to
// nvidiaSMIWait for the answer, returning the last one if it takes longer.
// While a call is stuck (see nvidiaSMIStuck), it returns none and starts no
// other. While the GPUs sleep, it starts none either, which would wake them,
// and returns the last answer as idle. A nil nvidiaSMI reads none.
func (n *nvidiaSMI) read(ctx context.Context) []GPU {
	if n == nil || n.program == "" {
		return nil
	}
	if n.sleep.Asleep() {
		n.mu.Lock()
		defer n.mu.Unlock()
		return idleGPUs(n.gpus)
	}
	n.mu.Lock()
	if n.running == nil && time.Since(n.at) >= nvidiaSMIInterval {
		n.running, n.started = make(chan struct{}), time.Now()
		go n.ask(context.WithoutCancel(ctx), n.running)
	}
	if n.running != nil && time.Since(n.started) > nvidiaSMITimeout+nvidiaSMIStuck {
		if !n.stuck {
			slog.Warn("nvidia-smi does not end; reporting no NVIDIA GPU until it does", "since", n.started)
			n.stuck = true
		}
		n.mu.Unlock()
		return nil
	}
	running := n.running
	n.mu.Unlock()

	if running != nil {
		wait := time.NewTimer(nvidiaSMIWait)
		select {
		case <-running:
		case <-wait.C:
		case <-ctx.Done():
		}
		wait.Stop()
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.gpus
}

// ask runs nvidia-smi, keeps its answer and closes done. ctx must not end
// with the reading that asked, which may end before nvidia-smi does.
func (n *nvidiaSMI) ask(ctx context.Context, done chan struct{}) {
	query := n.query
	if query == nil {
		query = readNvidiaSMI
	}
	ctx, cancel := context.WithTimeout(ctx, nvidiaSMITimeout)
	gpus := query(ctx, n.program)
	cancel()

	n.mu.Lock()
	n.gpus, n.at, n.running = gpus, time.Now(), nil
	if n.stuck {
		slog.Info("nvidia-smi ended again")
		n.stuck = false
	}
	n.mu.Unlock()
	close(done)
}

// idleGPUs returns GPUs as they are while they sleep: unused, with no memory
// in use and no temperature, which only an awake GPU reports.
func idleGPUs(gpus []GPU) []GPU {
	var idle []GPU
	for _, gpu := range gpus {
		gpu.UsagePercent, gpu.MemoryUsedBytes, gpu.Celsius = 0, 0, nil
		idle = append(idle, gpu)
	}
	return idle
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

// readNvidiaSMI asks nvidia-smi for the usage of the NVIDIA GPUs, giving up
// when ctx ends.
func readNvidiaSMI(ctx context.Context, program string) []GPU {
	query := "--query-gpu=name,utilization.gpu,memory.used,memory.total,temperature.gpu"
	// program is the nvidia-smi found on the PATH at start, and the arguments are fixed.
	cmd := exec.CommandContext(ctx, program, query, "--format=csv,noheader,nounits")
	HideWindow(cmd)
	// Once nvidia-smi is killed, wait at most a second for its output to
	// close, in case a child it started keeps it open.
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	// When one GPU is in an error state, nvidia-smi still prints the others
	// but exits with an error, so what it printed is read then too; not
	// when it was killed, as its output may end in the middle of a line.
	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || ctx.Err() != nil) {
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
