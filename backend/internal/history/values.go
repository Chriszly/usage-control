package history

import "github.com/Chriszly/usage-control/backend/internal/metrics"

// Metric names. Values that exist once per disk, sensor or network interface
// get its name after a colon, such as "disk:/" or "network.receive:eth0".
const (
	MetricCPU            = "cpu"
	MetricMemory         = "memory"
	MetricSwap           = "swap"
	MetricBattery        = "battery"
	MetricTemperature    = "temperature"
	MetricDisk           = "disk"
	MetricDiskRead       = "disk.read"
	MetricDiskWrite      = "disk.write"
	MetricNetworkReceive = "network.receive"
	MetricNetworkSend    = "network.send"
	MetricGPU            = "gpu"
	MetricGPUMemory      = "gpu.memory"
)

// values returns the values of a snapshot that are kept in the history:
// usage in percent (also of swap and GPU memory), battery charge in percent,
// temperatures in °C and disk and network speeds in bytes per second. The load average, clock, each
// core's usage and throttling are only shown live.
func values(s metrics.Snapshot) map[string]float64 {
	v := map[string]float64{
		MetricCPU:    s.CPU.UsagePercent,
		MetricMemory: s.Memory.UsedPercent,
	}
	if s.Memory.Swap != nil {
		v[MetricSwap] = s.Memory.Swap.UsedPercent
	}
	if s.Battery != nil {
		v[MetricBattery] = s.Battery.Percent
	}
	for _, t := range s.Temperatures {
		v[MetricTemperature+":"+t.Sensor] = t.Celsius
	}
	for _, d := range s.Disks {
		v[MetricDisk+":"+d.Path] = d.UsedPercent
		if d.ReadBytesPerSecond != nil && d.WriteBytesPerSecond != nil {
			v[MetricDiskRead+":"+d.Path] = *d.ReadBytesPerSecond
			v[MetricDiskWrite+":"+d.Path] = *d.WriteBytesPerSecond
		}
	}
	for _, n := range s.Network {
		v[MetricNetworkReceive+":"+n.Name] = n.ReceiveBytesPerSecond
		v[MetricNetworkSend+":"+n.Name] = n.SendBytesPerSecond
	}
	for _, g := range s.GPUs {
		v[MetricGPU+":"+g.Name] = g.UsagePercent
		if memory, ok := g.MemoryUsedPercent(); ok {
			v[MetricGPUMemory+":"+g.Name] = memory
		}
	}
	return v
}
