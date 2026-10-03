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

// DefaultMaxEntries is how many disks, temperature sensors, network cards and
// GPUs each the history keeps per device unless HISTORY_MAX_ENTRIES says
// otherwise: more than any real machine has, and a bound on what another
// device's answer can make a hub store.
const DefaultMaxEntries = 64

// values returns the values of a snapshot that are kept in the history:
// usage in percent (also of swap and GPU memory), battery charge in percent,
// temperatures in °C and disk and network speeds in bytes per second. The load average, clock, each
// core's usage and throttling are only shown live. Of the disks, sensors,
// network cards and GPUs, the first maxEntries each are kept; dropped tells
// whether any were left out.
func values(s metrics.Snapshot, maxEntries int) (v map[string]float64, dropped bool) {
	v = map[string]float64{
		MetricCPU:    s.CPU.UsagePercent,
		MetricMemory: s.Memory.UsedPercent,
	}
	if s.Memory.Swap != nil {
		v[MetricSwap] = s.Memory.Swap.UsedPercent
	}
	if s.Battery != nil {
		v[MetricBattery] = s.Battery.Percent
	}
	for _, t := range first(s.Temperatures, maxEntries, &dropped) {
		v[MetricTemperature+":"+t.Sensor] = t.Celsius
	}
	for _, d := range first(s.Disks, maxEntries, &dropped) {
		v[MetricDisk+":"+d.Path] = d.UsedPercent
		if d.ReadBytesPerSecond != nil && d.WriteBytesPerSecond != nil {
			v[MetricDiskRead+":"+d.Path] = *d.ReadBytesPerSecond
			v[MetricDiskWrite+":"+d.Path] = *d.WriteBytesPerSecond
		}
	}
	for _, n := range first(s.Network, maxEntries, &dropped) {
		v[MetricNetworkReceive+":"+n.Name] = n.ReceiveBytesPerSecond
		v[MetricNetworkSend+":"+n.Name] = n.SendBytesPerSecond
	}
	for _, g := range first(s.GPUs, maxEntries, &dropped) {
		v[MetricGPU+":"+g.Name] = g.UsagePercent
		if memory, ok := g.MemoryUsedPercent(); ok {
			v[MetricGPUMemory+":"+g.Name] = memory
		}
	}
	return v, dropped
}

// first returns the first n entries of list, and sets dropped when that
// leaves some out.
func first[T any](list []T, n int, dropped *bool) []T {
	if len(list) <= n {
		return list
	}
	*dropped = true
	return list[:n]
}
