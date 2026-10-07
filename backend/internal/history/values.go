package history

import (
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

// extrasPerEntry times the number of entries kept is how many values of
// extras the history keeps per device: 512 by default, room for every add-on
// on a host with dozens of containers, disks and file systems, where the
// number of groups times the number of values in each would allow the square.
const extrasPerEntry = 8

// MaxMetricLength is the longest metric name stored. A disk, sensor, network
// card or GPU whose name makes a longer one is left out of the history, so a
// broken device cannot fill the hub's memory and database with long names.
const MaxMetricLength = 256

// values returns the values of a snapshot that are kept in the history:
// usage in percent (also of swap and GPU memory), battery charge in percent,
// temperatures in °C, disk and network speeds in bytes per second, and the
// extras that ask for it in their own unit. The load average, clock, each
// core's usage and throttling are only shown live. Of the disks, sensors,
// network cards and GPUs whose names fit in MaxMetricLength, the first
// maxEntries each are kept, and of the extras the first extrasPerEntry times
// maxEntries; dropped tells whether any were left out for those limits, and
// tooLong whether any were for their names.
func values(s metrics.Snapshot, maxEntries int) (v map[string]float64, dropped, tooLong bool) {
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
	temperatures := named(s.Temperatures, MetricTemperature, func(t metrics.Temperature) string { return t.Sensor }, &tooLong)
	for _, t := range first(temperatures, maxEntries, &dropped) {
		v[MetricTemperature+":"+t.Sensor] = t.Celsius
	}
	disks := named(s.Disks, MetricDiskWrite, func(d metrics.Disk) string { return d.Path }, &tooLong)
	for _, d := range first(disks, maxEntries, &dropped) {
		v[MetricDisk+":"+d.Path] = d.UsedPercent
		if d.ReadBytesPerSecond != nil && d.WriteBytesPerSecond != nil {
			v[MetricDiskRead+":"+d.Path] = *d.ReadBytesPerSecond
			v[MetricDiskWrite+":"+d.Path] = *d.WriteBytesPerSecond
		}
	}
	network := named(s.Network, MetricNetworkReceive, func(n metrics.NetworkInterface) string { return n.Name }, &tooLong)
	for _, n := range first(network, maxEntries, &dropped) {
		v[MetricNetworkReceive+":"+n.Name] = n.ReceiveBytesPerSecond
		v[MetricNetworkSend+":"+n.Name] = n.SendBytesPerSecond
	}
	gpus := named(s.GPUs, MetricGPUMemory, func(g metrics.GPU) string { return g.Name }, &tooLong)
	for _, g := range first(gpus, maxEntries, &dropped) {
		v[MetricGPU+":"+g.Name] = g.UsagePercent
		if memory, ok := g.MemoryUsedPercent(); ok {
			v[MetricGPUMemory+":"+g.Name] = memory
		}
	}
	extras, extrasDropped := storedExtras(s.Extras, maxEntries)
	for _, extra := range extras {
		v[extra.metric] = *extra.item.Value
	}
	return v, dropped || extrasDropped, tooLong
}

// storedExtra is a value of an extra that is kept in the history, with its
// group and the metric it is stored under.
type storedExtra struct {
	metric string
	group  metrics.Extra
	item   metrics.ExtraItem
}

// storedExtras returns the values of the extras that ask for their history,
// the first extrasPerEntry times maxEntries of them, and whether that left
// some out.
func storedExtras(extras []metrics.Extra, maxEntries int) (stored []storedExtra, dropped bool) {
	for _, group := range extras {
		for _, item := range group.Items {
			if !item.History || item.Value == nil {
				continue
			}
			if len(stored) == extrasPerEntry*maxEntries {
				return stored, true
			}
			stored = append(stored, storedExtra{metric: extraMetric(group, item), group: group, item: item})
		}
	}
	return stored, false
}

// extraInfo describes the extras values keeps, by the metric they are stored
// under.
func extraInfo(extras []metrics.Extra, maxEntries int) map[string]ExtraInfo {
	stored, _ := storedExtras(extras, maxEntries)
	info := map[string]ExtraInfo{}
	for _, extra := range stored {
		info[extra.metric] = ExtraInfo{
			Title:  extra.group.Title,
			Titles: extra.group.Titles,
			Label:  extra.item.Label,
			Labels: extra.item.Labels,
			Unit:   extra.item.Unit,
		}
	}
	return info
}

func extraMetric(group metrics.Extra, item metrics.ExtraItem) string {
	return MetricExtra + ":" + group.ID + "/" + item.ID
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

// named returns the entries of list whose name makes metric names of at most
// MaxMetricLength, after prefix, the longest of their kind's metrics, and
// sets dropped when that leaves some out.
func named[T any](list []T, prefix string, name func(T) string, dropped *bool) []T {
	tooLong := func(entry T) bool { return len(prefix)+1+len(name(entry)) > MaxMetricLength }
	if !slices.ContainsFunc(list, tooLong) {
		return list
	}
	*dropped = true
	return slices.DeleteFunc(slices.Clone(list), tooLong)
}
