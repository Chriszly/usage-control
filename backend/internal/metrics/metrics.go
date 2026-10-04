// Package metrics reads the current usage of the machine the program runs on.
//
// It only reads hardware data; nothing in here changes the machine. Inside a
// container, set HOST_PROC and HOST_SYS to where the host's /proc and /sys are
// mounted so the values describe the host instead of the container.
package metrics

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/version"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/sensors"
)

// Snapshot is the usage of the machine at one point in time.
type Snapshot struct {
	// Name is what the device calls itself, which a hub offers as the name
	// when the device is added. Left out when it is not known.
	Name string `json:"name,omitempty"`
	// Version is the version of usage-control that read the usage. Devices
	// running a version from before it was reported leave it out.
	Version string    `json:"version,omitempty"`
	Time    time.Time `json:"time"`
	// TimeZone is the zone the device's clock is set to. Devices running a
	// version from before it was reported leave it out.
	TimeZone      *TimeZone          `json:"timeZone,omitempty"`
	UptimeSeconds uint64             `json:"uptimeSeconds"`
	CPU           CPU                `json:"cpu"`
	Memory        Memory             `json:"memory"`
	Temperatures  []Temperature      `json:"temperatures"`
	Disks         []Disk             `json:"disks"`
	Network       []NetworkInterface `json:"network"`
	GPUs          []GPU              `json:"gpus"`
	// Throttling is only reported by Raspberry Pis.
	Throttling *Throttling `json:"throttling,omitempty"`
	// Battery is only reported by machines with a battery.
	Battery *Battery `json:"battery,omitempty"`
	// Fans is only reported where Linux knows the fans, such as on a
	// Raspberry Pi 5 with its cooling fan.
	Fans []Fan `json:"fans,omitempty"`
}

// CPU is the processor usage across all cores and of each core. The clock,
// the load average, I/O wait, steal and the processes are left out where the
// OS does not report them.
type CPU struct {
	UsagePercent     float64      `json:"usagePercent"`
	Cores            int          `json:"cores"`
	CoreUsagePercent []float64    `json:"coreUsagePercent,omitempty"`
	ClockMHz         float64      `json:"clockMHz,omitempty"`
	LoadAverage      *LoadAverage `json:"loadAverage,omitempty"`
	IOWaitPercent    *float64     `json:"ioWaitPercent,omitempty"`
	StealPercent     *float64     `json:"stealPercent,omitempty"`
	Processes        *Processes   `json:"processes,omitempty"`
}

// Memory is the usage of the main memory (RAM), and of the swap space when
// the machine has one. Available is what programs can still get, including
// the cache the system frees when needed; the cache is only reported by
// Linux.
type Memory struct {
	TotalBytes     uint64  `json:"totalBytes"`
	UsedBytes      uint64  `json:"usedBytes"`
	UsedPercent    float64 `json:"usedPercent"`
	AvailableBytes uint64  `json:"availableBytes,omitempty"`
	CachedBytes    uint64  `json:"cachedBytes,omitempty"`
	Swap           *Swap   `json:"swap,omitempty"`
}

// Temperature is the reading of one temperature sensor.
type Temperature struct {
	Sensor  string  `json:"sensor"`
	Celsius float64 `json:"celsius"`
}

// Collector reads snapshots of the machine's usage.
type Collector struct {
	// Name is reported as the Snapshot's Name.
	Name string

	diskPaths      []string
	gpus           *gpuReader
	batteries      *batteryReader
	clockFiles     []string
	throttlingFile string
	fans           []fanSensor

	// mu guards the readings of the previous call, which CPU usage and
	// network and disk speeds are measured against.
	mu              sync.Mutex
	cpuTimes        cpu.TimesStat
	coreTimes       []cpu.TimesStat
	networkCounters map[string]counters
	networkTime     time.Time
	diskCounters    map[string]ioCounters
	diskTime        time.Time

	// linkMu guards the speed and addresses of the network interfaces,
	// which are read again every linkInterval.
	linkMu   sync.Mutex
	links    map[string]link
	linkTime time.Time
}

// NewCollector returns a Collector for the machine the program runs on that
// reports the disk usage of the filesystems holding diskPaths. It fails when
// one of the paths cannot be read.
func NewCollector(ctx context.Context, diskPaths []string) (*Collector, error) {
	if err := checkDiskPaths(ctx, diskPaths); err != nil {
		return nil, err
	}
	return &Collector{
		diskPaths:      diskPaths,
		gpus:           newGPUReader(),
		batteries:      newBatteryReader(),
		clockFiles:     clockFiles(),
		throttlingFile: throttlingFile(),
		fans:           fanSensors(),
	}, nil
}

// Collect reads the current usage of the machine.
//
// CPU usage and network and disk speeds are measured since the previous call,
// so the first call after start reports the average CPU usage since the
// machine booted and speeds of 0.
func (c *Collector) Collect(ctx context.Context) (Snapshot, error) {
	cpuUsage, err := c.readCPUUsage(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read CPU usage: %w", err)
	}
	memory, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read memory usage: %w", err)
	}
	uptime, err := host.UptimeWithContext(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read uptime: %w", err)
	}
	network, err := c.readNetwork(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read network traffic: %w", err)
	}
	c.addLinks(network)

	now := time.Now()
	cpuUsage.ClockMHz = readClockMHz(c.clockFiles)
	cpuUsage.LoadAverage, cpuUsage.Processes = readLoadAverage(ctx)

	return Snapshot{
		Name:          c.Name,
		Version:       version.Version,
		Time:          now.UTC(),
		TimeZone:      timeZone(now),
		UptimeSeconds: uptime,
		CPU:           cpuUsage,
		Memory: Memory{
			TotalBytes:     memory.Total,
			UsedBytes:      memory.Used,
			UsedPercent:    memory.UsedPercent,
			AvailableBytes: memory.Available,
			CachedBytes:    memory.Cached + memory.Buffers,
			Swap:           readSwap(ctx, memory),
		},
		Temperatures: append(readTemperatures(ctx), c.gpus.temperatures(ctx)...),
		Disks:        c.readDisks(ctx),
		Network:      network,
		GPUs:         c.gpus.read(ctx),
		Throttling:   readThrottling(c.throttlingFile),
		Battery:      c.batteries.read(),
		Fans:         readFans(c.fans),
	}, nil
}

// readDisks returns the usage of each disk, with its activity measured since
// the previous call.
func (c *Collector) readDisks(ctx context.Context) []Disk {
	disks := readDisks(ctx, c.diskPaths)
	current := readDiskCounters(ctx, c.diskPaths)
	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()
	diskActivity(c.diskCounters, current, now.Sub(c.diskTime), disks)
	c.diskCounters, c.diskTime = current, now
	return disks
}

// readNetwork returns the traffic of each network interface, with the speed
// measured since the previous call.
func (c *Collector) readNetwork(ctx context.Context) ([]NetworkInterface, error) {
	current, err := readNetworkCounters(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()
	interfaces := throughput(c.networkCounters, current, now.Sub(c.networkTime))
	c.networkCounters, c.networkTime = current, now
	return interfaces, nil
}

// readTemperatures returns every sensor reading the OS exposes. Many machines
// expose none (most Windows and macOS machines, and most containers without
// the host's /sys), so a failure means an empty list, not an error.
func readTemperatures(ctx context.Context) []Temperature {
	// gopsutil returns partial results together with an error when some
	// sensors cannot be read, so the readings are used even if err is set.
	readings, _ := sensors.TemperaturesWithContext(ctx)
	return temperaturesOf(readings)
}

// temperaturesOf turns the sensor readings into temperatures, leaving out the
// sensors that report none. Sensors with the same name, such as one coretemp
// per core, are numbered, so each name stands for one sensor in the history.
func temperaturesOf(readings []sensors.TemperatureStat) []Temperature {
	temperatures := make([]Temperature, 0, len(readings))
	names := make([]string, 0, len(readings))
	for _, r := range readings {
		if r.Temperature <= 0 {
			continue
		}
		temperatures = append(temperatures, Temperature{Sensor: r.SensorKey, Celsius: r.Temperature})
		names = append(names, r.SensorKey)
	}
	for i, name := range numberDuplicates(names) {
		temperatures[i].Sensor = name
	}
	return temperatures
}
