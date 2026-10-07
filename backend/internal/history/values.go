package history

import (
	"cmp"
	"slices"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

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
	// MetricExtra is followed by the group and the value, such as
	// "extra:pressure/cpu"; see metrics.Extra.
	MetricExtra = "extra"
)

// DefaultMaxEntries is how many disks, temperature sensors, network cards and
// GPUs each the history keeps per device unless HISTORY_MAX_ENTRIES says
// otherwise: more than any real machine has, and a bound on what another
// device's answer can make a hub store.
const DefaultMaxEntries = 64

// values returns the values of a snapshot that are kept in the history:
// usage in percent (also of swap and GPU memory), battery charge in percent,
// temperatures in °C, disk and network speeds in bytes per second, and the
// extras that ask for it in their own unit. The load average, clock, each
// core's usage and throttling are only shown live. Of the disks, sensors,
// network cards and GPUs, the first maxEntries each by name are kept, as a
// hub keeps of the minutes it fetches; dropped tells whether any were left
// out.
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
	for _, t := range first(s.Temperatures, func(t metrics.Temperature) string { return t.Sensor }, maxEntries, &dropped) {
		v[MetricTemperature+":"+t.Sensor] = t.Celsius
	}
	for _, d := range first(s.Disks, func(d metrics.Disk) string { return d.Path }, maxEntries, &dropped) {
		v[MetricDisk+":"+d.Path] = d.UsedPercent
		if d.ReadBytesPerSecond != nil && d.WriteBytesPerSecond != nil {
			v[MetricDiskRead+":"+d.Path] = *d.ReadBytesPerSecond
			v[MetricDiskWrite+":"+d.Path] = *d.WriteBytesPerSecond
		}
	}
	for _, n := range first(s.Network, func(n metrics.NetworkInterface) string { return n.Name }, maxEntries, &dropped) {
		v[MetricNetworkReceive+":"+n.Name] = n.ReceiveBytesPerSecond
		v[MetricNetworkSend+":"+n.Name] = n.SendBytesPerSecond
	}
	for _, g := range first(s.GPUs, func(g metrics.GPU) string { return g.Name }, maxEntries, &dropped) {
		v[MetricGPU+":"+g.Name] = g.UsagePercent
		if memory, ok := g.MemoryUsedPercent(); ok {
			v[MetricGPUMemory+":"+g.Name] = memory
		}
	}
	for metric, value := range extraValues(s.Extras) {
		v[metric] = value
	}
	return v, dropped
}

// extraValues returns the values of the extras that ask for their history.
func extraValues(extras []metrics.Extra) map[string]float64 {
	v := map[string]float64{}
	for _, group := range extras {
		for _, item := range group.Items {
			if item.History && item.Value != nil {
				v[extraMetric(group, item)] = *item.Value
			}
		}
	}
	return v
}

// extraInfo describes the extras that ask for their history, by the metric
// they are stored under.
func extraInfo(extras []metrics.Extra) map[string]ExtraInfo {
	info := map[string]ExtraInfo{}
	for _, group := range extras {
		for _, item := range group.Items {
			if item.History && item.Value != nil {
				info[extraMetric(group, item)] = ExtraInfo{
					Title:  group.Title,
					Titles: group.Titles,
					Label:  item.Label,
					Labels: item.Labels,
					Unit:   item.Unit,
				}
			}
		}
	}
	return info
}

func extraMetric(group metrics.Extra, item metrics.ExtraItem) string {
	return MetricExtra + ":" + group.ID + "/" + item.ID
}

// first returns the first n entries of list by name, and sets dropped when
// that leaves some out.
func first[T any](list []T, name func(T) string, n int, dropped *bool) []T {
	if len(list) <= n {
		return list
	}
	*dropped = true
	sorted := slices.SortedFunc(slices.Values(list), func(a, b T) int { return cmp.Compare(name(a), name(b)) })
	return sorted[:n]
}
