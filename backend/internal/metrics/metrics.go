// Package metrics reads the current usage of the machine the program runs on.
//
// It only reads hardware data; nothing in here changes the machine. Inside a
// container, set HOST_PROC and HOST_SYS to where the host's /proc and /sys are
// mounted so the values describe the host instead of the container.
package metrics

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
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
	// Extras are values beyond the fields above, described well enough that
	// a hub can show them without knowing them; see Extra.
	Extras []Extra `json:"extras,omitempty"`
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
	// AddOns are read for the Snapshot's Extras; nil reads none.
	AddOns *AddOns

	diskPaths      []string
	gpus           *gpuReader
	batteries      *batteryReader
	clockFiles     []string
	throttlingFile string
	fans           []fanSensor

	// readCounters reads the network counters; nil reads the machine's.
	readCounters func(ctx context.Context) (map[string]counters, error)
	temperatures *temperatureReader

	// mu guards the readings of the previous call, which CPU usage and
	// network and disk speeds are measured against, and whether reading
	// the network failed last time.
	mu              sync.Mutex
	cpuTimes        cpu.TimesStat
	coreTimes       []cpu.TimesStat
	networkCounters map[string]counters
	networkTime     time.Time
	diskCounters    map[string]ioCounters
	diskTime        time.Time
	networkFailing  bool

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
		temperatures:   newTemperatureReader(),
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
	network := c.readNetwork(ctx)
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
		Temperatures: append(c.temperatures.read(ctx, now), c.gpus.temperatures(ctx)...),
		Disks:        c.readDisks(ctx),
		Network:      network,
		GPUs:         c.gpus.read(ctx),
		Throttling:   readThrottling(c.throttlingFile),
		Battery:      c.batteries.read(),
		Fans:         readFans(c.fans),
		Extras:       c.AddOns.Read(now),
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
// measured since the previous call. When the counters cannot be read, the
// rest of the reading is still worth having, so the list is empty instead;
// that is logged once, and the next reading measures its speed since the
// last one that worked.
func (c *Collector) readNetwork(ctx context.Context) []NetworkInterface {
	read := c.readCounters
	if read == nil {
		read = readNetworkCounters
	}
	current, err := read(ctx)
	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case err != nil && !c.networkFailing:
		slog.Warn("read network traffic; reporting no network until it works again", "error", err)
	case err == nil && c.networkFailing:
		slog.Info("reading network traffic works again")
	}
	c.networkFailing = err != nil
	if err != nil {
		return []NetworkInterface{}
	}
	interfaces := throughput(c.networkCounters, current, now.Sub(c.networkTime))
	c.networkCounters, c.networkTime = current, now
	return interfaces
}

// temperatureRetry is how long the temperature sensors are left alone after
// a reading found none, on Windows: there gopsutil asks WMI's
// MSAcpi_ThermalZoneTemperature, a costly query that in the Local Service
// account usually fails or finds nothing. Elsewhere reading the sensors is a
// few file reads, so they are read every time.
const temperatureRetry = 10 * time.Minute

// temperatureReader reads the temperature sensors, and on Windows, after a
// reading that found none, waits temperatureRetry before trying again.
type temperatureReader struct {
	// sensors reads the sensors; retry is how long to wait after finding none,
	// 0 to read every time.
	sensors func(ctx context.Context) []Temperature
	retry   time.Duration

	mu      sync.Mutex
	retryAt time.Time
}

func newTemperatureReader() *temperatureReader {
	r := &temperatureReader{sensors: readTemperatures}
	if runtime.GOOS == "windows" {
		r.retry = temperatureRetry
	}
	return r
}

// read returns the temperatures at now, or none while it waits to try again.
// A nil temperatureReader reads the sensors every time.
func (r *temperatureReader) read(ctx context.Context, now time.Time) []Temperature {
	if r == nil {
		return readTemperatures(ctx)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if now.Before(r.retryAt) {
		return []Temperature{}
	}
	temperatures := r.sensors(ctx)
	if len(temperatures) == 0 && r.retry > 0 {
		r.retryAt = now.Add(r.retry)
	}
	return temperatures
}

// readTemperatures returns every sensor reading the OS exposes. Many machines
// expose none (most Windows and macOS machines, and most containers without
// the host's /sys), so a failure means an empty list, not an error.
func readTemperatures(ctx context.Context) []Temperature {
	// gopsutil returns partial results together with an error when some
	// sensors cannot be read, so the readings are used even if err is set.
	readings, _ := readSensors(ctx)
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
	for i, name := range NumberDuplicates(names) {
		temperatures[i].Sensor = name
	}
	return temperatures
}
