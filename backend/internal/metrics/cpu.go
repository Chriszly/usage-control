package metrics

import (
	"context"
	"errors"
	"runtime"

	"github.com/shirou/gopsutil/v4/cpu"
)

// readCPUUsage returns the share of time the processor was busy since the
// previous call, in percent. Each Collector keeps its own previous reading, so
// the dashboard and the history recorder measure their own intervals.
func (c *Collector) readCPUUsage(ctx context.Context) (float64, error) {
	times, err := cpu.TimesWithContext(ctx, false)
	if err != nil {
		return 0, err
	}
	if len(times) == 0 {
		return 0, errors.New("the OS reported no CPU times")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	usage := busyPercent(c.cpuTimes, times[0])
	c.cpuTimes = times[0]
	return usage, nil
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
