package metrics

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"

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

// readCPUUsage returns the share of time the processor was busy since the
// previous call, in percent, in total and per core. Each Collector keeps its
// own previous reading, so the dashboard and the history recorder measure
// their own intervals. Where the OS does not report each core, only the total
// is returned.
func (c *Collector) readCPUUsage(ctx context.Context) (total float64, cores []float64, err error) {
	// The times of each core come from the same file or call as the total, so
	// reading them costs no more than the total alone.
	perCore, err := cpu.TimesWithContext(ctx, true)
	if err != nil || len(perCore) == 0 {
		perCore = nil
	}
	var sum cpu.TimesStat
	if perCore != nil {
		sum = sumTimes(perCore)
	} else {
		times, err := cpu.TimesWithContext(ctx, false)
		if err != nil {
			return 0, nil, err
		}
		if len(times) == 0 {
			return 0, nil, errors.New("the OS reported no CPU times")
		}
		sum = times[0]
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	total = busyPercent(c.cpuTimes, sum)
	if perCore != nil {
		previous := c.coreTimes
		if len(previous) != len(perCore) {
			// The first reading, or a core was switched on or off.
			previous = make([]cpu.TimesStat, len(perCore))
		}
		cores = make([]float64, len(perCore))
		for i, times := range perCore {
			cores[i] = busyPercent(previous[i], times)
		}
	}
	c.cpuTimes, c.coreTimes = sum, perCore
	return total, cores, nil
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

// readLoadAverage returns the load average, or nil where the OS has none.
// Windows has no load average; gopsutil imitates one with a background
// sampler, which costs more than it tells, so Windows shows none.
func readLoadAverage(ctx context.Context) *LoadAverage {
	if runtime.GOOS == "windows" {
		return nil
	}
	avg, err := load.AvgWithContext(ctx)
	if err != nil {
		return nil
	}
	return &LoadAverage{One: avg.Load1, Five: avg.Load5, Fifteen: avg.Load15}
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
		kHz, err := readUint(file)
		if err != nil {
			continue
		}
		highest = max(highest, kHz)
	}
	return float64(highest) / 1000
}
