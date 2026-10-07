package history

import (
	"maps"
	"math"
	"regexp"
	"slices"
	"strings"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// single are the metrics a device has once.
var single = map[string]bool{
	MetricCPU: true, MetricMemory: true, MetricSwap: true, MetricBattery: true,
}

// perEntry names, for the metrics that exist once per disk, sensor, network
// card or GPU, what they exist once per, by the longest of its kind's metrics:
// one whose name makes that longer than MaxMetricLength is left out with all
// its metrics, as the recorder leaves it out.
var perEntry = map[string]string{
	MetricTemperature:    MetricTemperature,
	MetricDisk:           MetricDiskWrite,
	MetricDiskRead:       MetricDiskWrite,
	MetricDiskWrite:      MetricDiskWrite,
	MetricNetworkReceive: MetricNetworkReceive,
	MetricNetworkSend:    MetricNetworkReceive,
	MetricGPU:            MetricGPUMemory,
	MetricGPUMemory:      MetricGPUMemory,
}

// validKind matches a metric this version does not know, or what comes before
// the colon of one, as a device of a newer version may keep: a short plain
// name, as the known ones are.
var validKind = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,39}$`)

// unknown is the kind of entry each metric this version does not know, or
// each kind of them, is.
const unknown = ""

// entry is one disk, sensor, network card, GPU, group of extras, extra, or
// metric or kind of metrics this version does not know: kind says which (for
// an extra, its group), name which one.
type entry struct{ kind, name string }

// Keep returns the values of one minute a hub keeps of the minutes it fetches
// from a device, as a recorder keeps of its readings: values that can be
// stored, of at most maxEntries disks, sensors, network cards and GPUs each
// whose names fit in MaxMetricLength, and of the extras at most MaxExtras, of
// at most maxEntries groups of maxEntries extras each. Metrics this version
// does not know, as from a device of a newer version, are kept too, once
// they would be by their name, so a hub that is updated later shows them:
// at most maxEntries of them or kinds of them, maxEntries of each kind, and
// MaxExtras in all. Where there are more, the first by name are kept, so
// every minute keeps the same ones; dropped tells whether any were left out
// for those limits, and tooLong whether any were for their names.
func Keep(values map[string]float64, maxEntries int) (kept map[string]float64, dropped, tooLong bool) {
	entries := map[string][]entry{}
	names := map[string]map[string]bool{}
	for metric, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		of, ok, long := entriesOf(metric)
		tooLong = tooLong || long
		if !ok {
			continue
		}
		entries[metric] = of
		for _, e := range of {
			if names[e.kind] == nil {
				names[e.kind] = map[string]bool{}
			}
			names[e.kind][e.name] = true
		}
	}
	keptEntries := map[entry]bool{}
	for kind, set := range names {
		sorted := slices.Sorted(maps.Keys(set))
		if len(sorted) > maxEntries {
			sorted, dropped = sorted[:maxEntries], true
		}
		for _, name := range sorted {
			keptEntries[entry{kind, name}] = true
		}
	}
	kept = map[string]float64{}
	var extras, others []string
	for metric, of := range entries {
		if slices.ContainsFunc(of, func(e entry) bool { return !keptEntries[e] }) {
			continue
		}
		switch {
		case strings.HasPrefix(metric, MetricExtra+":"):
			extras = append(extras, metric)
		case len(of) > 0 && of[0].kind == unknown:
			others = append(others, metric)
		default:
			kept[metric] = values[metric]
		}
	}
	for _, list := range [][]string{extras, others} {
		if len(list) > MaxExtras(maxEntries) {
			slices.Sort(list)
			list, dropped = list[:MaxExtras(maxEntries)], true
		}
		for _, metric := range list {
			kept[metric] = values[metric]
		}
	}
	return kept, dropped, tooLong
}

// entriesOf returns what a metric belongs to, and false for one the history
// does not keep; tooLong tells that is for its name.
func entriesOf(metric string) (of []entry, ok, tooLong bool) {
	if single[metric] {
		return nil, true, false
	}
	kind, name, ok := strings.Cut(metric, ":")
	if !ok {
		if !validKind.MatchString(metric) {
			return nil, false, false
		}
		return []entry{{unknown, metric}}, true, false
	}
	if name == "" {
		return nil, false, false
	}
	if kind == MetricExtra {
		group, item, ok := strings.Cut(name, "/")
		if !ok || !metrics.ValidID(group) || !metrics.ValidID(item) {
			return nil, false, false
		}
		return []entry{{kind, group}, {kind + ":" + group, item}}, true, false
	}
	longest, ok := perEntry[kind]
	if !ok {
		if single[kind] || !validKind.MatchString(kind) {
			return nil, false, false
		}
		if len(metric) > MaxMetricLength {
			return nil, false, true
		}
		return []entry{{unknown, kind}, {kind, name}}, true, false
	}
	if len(longest)+1+len(name) > MaxMetricLength {
		return nil, false, true
	}
	return []entry{{longest, name}}, true, false
}
