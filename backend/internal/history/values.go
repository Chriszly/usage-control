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

// extrasPerEntry times the number of entries kept is how many values of
// extras the history keeps per device: 512 by default, room for every add-on
// on a host with dozens of containers, disks and file systems, where the
// number of groups times the number of values in each would allow the square.
const extrasPerEntry = 8

// MaxExtras returns how many values of extras the history keeps per device
// when it keeps maxEntries disks, sensors, network cards and GPUs each: of
// more, the first by metric name, as a recorder keeps of its readings and a
// hub of the minutes it fetches.
func MaxExtras(maxEntries int) int {
	return extrasPerEntry * maxEntries
}

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
// maxEntries each by name are kept, as a hub keeps of the minutes it
// fetches, and of the extras the first MaxExtras by metric name;
// dropped tells whether any were left out for those limits, and tooLong
// whether any were for their names.
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
	for _, t := range first(temperatures, func(t metrics.Temperature) string { return t.Sensor }, maxEntries, &dropped) {
		v[MetricTemperature+":"+t.Sensor] = t.Celsius
	}
	disks := named(s.Disks, MetricDiskWrite, func(d metrics.Disk) string { return d.Path }, &tooLong)
	for _, d := range first(disks, func(d metrics.Disk) string { return d.Path }, maxEntries, &dropped) {
		v[MetricDisk+":"+d.Path] = d.UsedPercent
		if d.ReadBytesPerSecond != nil && d.WriteBytesPerSecond != nil {
			v[MetricDiskRead+":"+d.Path] = *d.ReadBytesPerSecond
			v[MetricDiskWrite+":"+d.Path] = *d.WriteBytesPerSecond
		}
	}
	network := named(s.Network, MetricNetworkReceive, func(n metrics.NetworkInterface) string { return n.Name }, &tooLong)
	for _, n := range first(network, func(n metrics.NetworkInterface) string { return n.Name }, maxEntries, &dropped) {
		v[MetricNetworkReceive+":"+n.Name] = n.ReceiveBytesPerSecond
		v[MetricNetworkSend+":"+n.Name] = n.SendBytesPerSecond
	}
	gpus := named(s.GPUs, MetricGPUMemory, func(g metrics.GPU) string { return g.Name }, &tooLong)
	for _, g := range first(gpus, func(g metrics.GPU) string { return g.Name }, maxEntries, &dropped) {
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
// the first MaxExtras of them by metric name, and whether that left some
// out.
func storedExtras(extras []metrics.Extra, maxEntries int) (stored []storedExtra, dropped bool) {
	for _, group := range extras {
		for _, item := range group.Items {
			if item.History && item.Value != nil {
				stored = append(stored, storedExtra{metric: extraMetric(group, item), group: group, item: item})
			}
		}
	}
	if len(stored) <= MaxExtras(maxEntries) {
		return stored, false
	}
	slices.SortFunc(stored, func(a, b storedExtra) int { return cmp.Compare(a.metric, b.metric) })
	return stored[:MaxExtras(maxEntries)], true
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
