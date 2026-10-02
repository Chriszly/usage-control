// Package metrics reads the current usage of the machine the program runs on.
//
// It only reads hardware data; nothing in here changes the machine. Inside a
// container, set HOST_PROC and HOST_SYS to where the host's /proc and /sys are
// mounted so the values describe the host instead of the container.
package metrics

import (
	"context"
	"fmt"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/sensors"
)

// Snapshot is the usage of the machine at one point in time.
type Snapshot struct {
	Time          time.Time     `json:"time"`
	UptimeSeconds uint64        `json:"uptimeSeconds"`
	CPU           CPU           `json:"cpu"`
	Memory        Memory        `json:"memory"`
	Temperatures  []Temperature `json:"temperatures"`
}

// CPU is the processor usage across all cores.
type CPU struct {
	UsagePercent float64 `json:"usagePercent"`
	Cores        int     `json:"cores"`
}

// Memory is the usage of the main memory (RAM).
type Memory struct {
	TotalBytes  uint64  `json:"totalBytes"`
	UsedBytes   uint64  `json:"usedBytes"`
	UsedPercent float64 `json:"usedPercent"`
}

// Temperature is the reading of one temperature sensor.
type Temperature struct {
	Sensor  string  `json:"sensor"`
	Celsius float64 `json:"celsius"`
}

// Collector reads snapshots of the machine's usage.
type Collector struct{}

// NewCollector returns a Collector for the machine the program runs on.
func NewCollector() *Collector {
	return &Collector{}
}

// Collect reads the current usage of the machine.
//
// CPU usage is measured since the previous call, so the first call after
// start reports the average since the machine booted.
func (c *Collector) Collect(ctx context.Context) (Snapshot, error) {
	cpuUsage, err := cpu.PercentWithContext(ctx, 0, false)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read CPU usage: %w", err)
	}
	cores, err := cpu.CountsWithContext(ctx, true)
	if err != nil {
		return Snapshot{}, fmt.Errorf("count CPU cores: %w", err)
	}
	memory, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read memory usage: %w", err)
	}
	uptime, err := host.UptimeWithContext(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read uptime: %w", err)
	}

	return Snapshot{
		Time:          time.Now().UTC(),
		UptimeSeconds: uptime,
		CPU: CPU{
			UsagePercent: firstOrZero(cpuUsage),
			Cores:        cores,
		},
		Memory: Memory{
			TotalBytes:  memory.Total,
			UsedBytes:   memory.Used,
			UsedPercent: memory.UsedPercent,
		},
		Temperatures: readTemperatures(ctx),
	}, nil
}

// readTemperatures returns every sensor reading the OS exposes. Many machines
// expose none (most Windows and macOS machines, and most containers without
// the host's /sys), so a failure means an empty list, not an error.
func readTemperatures(ctx context.Context) []Temperature {
	// gopsutil returns partial results together with an error when some
	// sensors cannot be read, so the readings are used even if err is set.
	readings, _ := sensors.TemperaturesWithContext(ctx)

	temperatures := make([]Temperature, 0, len(readings))
	for _, r := range readings {
		if r.Temperature <= 0 {
			continue
		}
		temperatures = append(temperatures, Temperature{Sensor: r.SensorKey, Celsius: r.Temperature})
	}
	return temperatures
}

func firstOrZero(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	return values[0]
}
