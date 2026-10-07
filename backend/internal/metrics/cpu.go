package metrics

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/Chriszly/usage-control/backend/internal/sysfile"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/load"
)

// LoadAverage is the average number of processes running or waiting for the
// processor over the last 1, 5 and 15 minutes.
type LoadAverage struct {
	One     float64 `json:"one"`
	Five    float64 `json:"five"`
	Fifteen float64 `json:"fifteen"`
}

// Processes is how many processes exist and how many of them are running
// or ready to run right now.
type Processes struct {
	Total   int `json:"total"`
	Running int `json:"running"`
}

// readCPUUsage returns the share of time the processor was busy since the
// previous call, in percent, in total and per core, the number of cores, and
// on Linux the share it waited for disks and, in a virtual machine, for the
// host (steal).
func (c *Collector) readCPUUsage(ctx context.Context) (CPU, error) {
	// The times of each core come from the same file or call as the total, so
	// reading them costs no more than the total alone.
	perCore, err := cpu.TimesWithContext(ctx, true)
	if err != nil {
		return CPU{}, err
	}
	if len(perCore) == 0 {
		return CPU{}, errors.New("the OS reported no CPU times")
	}
	sum := sumTimes(perCore)

	c.mu.Lock()
	defer c.mu.Unlock()
	usage := CPU{UsagePercent: busyPercent(c.cpuTimes, sum), Cores: len(perCore)}
	if runtime.GOOS == "linux" {
		ioWait, steal := waitPercents(c.cpuTimes, sum)
		usage.IOWaitPercent, usage.StealPercent = &ioWait, &steal
	}
	previous := c.coreTimes
	if len(previous) != len(perCore) {
		// The first reading, or a core was switched on or off.
		previous = make([]cpu.TimesStat, len(perCore))
	}
	usage.CoreUsagePercent = make([]float64, len(perCore))
	for i, times := range perCore {
		usage.CoreUsagePercent[i] = busyPercent(previous[i], times)
	}
	c.cpuTimes, c.coreTimes = sum, perCore
	return usage, nil
}

// waitPercents returns how much of the time between two readings the
// processor waited for disks (I/O wait) and for the host of a virtual machine
// (steal), in percent.
func waitPercents(previous, current cpu.TimesStat) (ioWait, steal float64) {
	previousTotal, _ := totalAndBusy(previous)
	currentTotal, _ := totalAndBusy(current)
	total := currentTotal - previousTotal
	if total <= 0 {
		return 0, 0
	}
	share := func(before, now float64) float64 { return min(100, max(0, now-before)/total*100) }
	return share(previous.Iowait, current.Iowait), share(previous.Steal, current.Steal)
}

// sumTimes adds up the times of every core.
func sumTimes(perCore []cpu.TimesStat) cpu.TimesStat {
	var s cpu.TimesStat
	for _, t := range perCore {
		s.User += t.User
		s.System += t.System
		s.Idle += t.Idle
		s.Nice += t.Nice
		s.Iowait += t.Iowait
		s.Irq += t.Irq
		s.Softirq += t.Softirq
		s.Steal += t.Steal
		s.Guest += t.Guest
		s.GuestNice += t.GuestNice
	}
	return s
}

// busyPercent returns how much of the time between two readings the processor
// was busy. A zero previous reading measures since the machine booted.
func busyPercent(previous, current cpu.TimesStat) float64 {
	previousTotal, previousBusy := totalAndBusy(previous)
	currentTotal, currentBusy := totalAndBusy(current)
	total := currentTotal - previousTotal
	if total <= 0 {
		return 0
	}
	busy := max(0, currentBusy-previousBusy)
	return min(100, busy/total*100)
}

func totalAndBusy(t cpu.TimesStat) (total, busy float64) {
	total = t.User + t.System + t.Idle + t.Nice + t.Iowait + t.Irq + t.Softirq + t.Steal
	if runtime.GOOS != "linux" {
		// Linux already counts guest time in User and Nice.
		total += t.Guest + t.GuestNice
	}
	return total, total - t.Idle - t.Iowait
}

// readLoadAverage returns the load average, or nil where the OS has none,
// and on Linux how many processes there are. Windows has no load average;
// gopsutil imitates one with a background sampler, which costs more than it
// tells, so Windows shows none.
func readLoadAverage(ctx context.Context) (*LoadAverage, *Processes) {
	switch runtime.GOOS {
	case "windows":
		return nil, nil
	case "linux":
		procDir := hostPath("HOST_PROC", "/proc")
		text, err := sysfile.Read(filepath.Join(procDir, "loadavg"))
		if err != nil {
			return nil, nil
		}
		avg, running, ok := parseLoadavg(string(text))
		if !ok {
			return nil, nil
		}
		total, err := countProcesses(procDir)
		if err != nil {
			return avg, nil
		}
		return avg, &Processes{Total: total, Running: running}
	default:
		avg, err := load.AvgWithContext(ctx)
		if err != nil {
			return nil, nil
		}
		return &LoadAverage{One: avg.Load1, Five: avg.Load5, Fifteen: avg.Load15}, nil
	}
}

// parseLoadavg reads the text of /proc/loadavg, such as
// "0.52 0.40 0.31 2/213 12345": the load average over 1, 5 and 15 minutes,
// then how many tasks are running or ready to run, of all tasks.
func parseLoadavg(text string) (avg *LoadAverage, running int, ok bool) {
	fields := strings.Fields(text)
	if len(fields) < 4 {
		return nil, 0, false
	}
	var values [3]float64
	for i := range values {
		value, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return nil, 0, false
		}
		values[i] = value
	}
	runningText, _, _ := strings.Cut(fields[3], "/")
	running, err := strconv.Atoi(runningText)
	if err != nil {
		return nil, 0, false
	}
	return &LoadAverage{One: values[0], Five: values[1], Fifteen: values[2]}, running, true
}

// countProcesses counts the processes by the directories Linux keeps for
// each of them in /proc, named by their number. Only the names are read.
func countProcesses(procDir string) (int, error) {
	dir, err := os.Open(procDir) //nolint:gosec // the host's /proc, not user input
	if err != nil {
		return 0, err
	}
	defer func() { _ = dir.Close() }()
	names, err := dir.Readdirnames(-1)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, name := range names {
		if name != "" && strings.Trim(name, "0123456789") == "" {
			count++
		}
	}
	return count, nil
}

// clockFiles returns the files Linux reports the current clock of each group
// of cores in (one per cpufreq policy, usually one for all cores). There are
// none on other systems, in most virtual machines and where the kernel does
// not scale the clock.
func clockFiles() []string {
	pattern := filepath.Join(hostPath("HOST_SYS", "/sys"), "devices", "system", "cpu", "cpufreq", "policy*", "scaling_cur_freq")
	files, _ := filepath.Glob(pattern)
	return files
}

// readClockMHz returns the highest current clock of the cores in MHz, or 0
// when none of the files can be read.
func readClockMHz(files []string) float64 {
	var highest uint64
	for _, file := range files {
		kHz, ok := sysfile.Uint(file)
		if !ok {
			continue
		}
		highest = max(highest, kHz)
	}
	return float64(highest) / 1000
}
