package history

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

func TestKeepKeepsWhatARecorderKeeps(t *testing.T) {
	minute := map[string]float64{
		"cpu": 1, "memory": 2, "swap": 3, "battery": 4,
		"temperature:b": 5, "temperature:a": 6, "temperature:c": 7,
		"disk:/b": 8, "disk.read:/b": 9, "disk.write:/b": 10, "disk:/a": 11, "disk.read:/c": 12,
		"network.receive:eth0": 13, "network.send:eth0": 14, "network.send:eth1": 15, "network.send:wlan0": 16,
		"gpu:one": 17, "gpu.memory:one": 18,
		"extra:power/cpu": 19, "extra:power/gpu": 20, "extra:power/soc": 21,
		"extra:alpha/one": 22, "extra:zeta/one": 23, "extra:Bad/one": 24,
		"unknown": 25, "fan:one": 26, "Bad:one": 27, "cpu:one": 28, "fan:": 29,
	}
	want := map[string]float64{
		"cpu": 1, "memory": 2, "swap": 3, "battery": 4,
		"temperature:a": 6, "temperature:b": 5,
		"disk:/b": 8, "disk.read:/b": 9, "disk.write:/b": 10, "disk:/a": 11,
		"network.receive:eth0": 13, "network.send:eth0": 14, "network.send:eth1": 15,
		"gpu:one": 17, "gpu.memory:one": 18,
		"extra:alpha/one": 22, "extra:power/cpu": 19, "extra:power/gpu": 20,
		// Metrics of a newer version, kept for when the hub is updated.
		"unknown": 25, "fan:one": 26,
	}
	// The same every time, not whichever the order of a map picks.
	for range 20 {
		got, dropped, tooLong := Keep(minute, 2)
		if !reflect.DeepEqual(got, want) || !dropped || tooLong {
			t.Fatalf("Keep() = %v, %v, %v; want %v, true, false", got, dropped, tooLong, want)
		}
	}
	if _, dropped, _ := Keep(map[string]float64{"cpu": 1, "disk:/a": 2}, 2); dropped {
		t.Error("Keep() of fewer entries than kept dropped some")
	}
}

func TestKeepKeepsAsManyExtrasAsARecorder(t *testing.T) {
	// 30 groups of 30 values each: within the groups and values per group
	// kept, but more values than the history keeps of extras in all.
	kept := map[string]float64{}
	var all []string
	for group := range 30 {
		for item := range 30 {
			metric := fmt.Sprintf("extra:g%02d/v%02d", group, item)
			kept[metric] = 1
			all = append(all, metric)
		}
	}
	got, dropped, _ := Keep(kept, 30)

	slices.Sort(all)
	want := map[string]float64{}
	for _, metric := range all[:MaxExtras(30)] {
		want[metric] = 1
	}
	if !reflect.DeepEqual(got, want) || !dropped {
		t.Errorf("Keep() kept %d extras, dropped %v; want the first %d by name, as the recorder keeps", len(got), dropped, MaxExtras(30))
	}
	if len(kept) <= MaxExtras(DefaultMaxEntries) {
		t.Fatalf("only %d extras, want more than the %d kept by default", len(kept), MaxExtras(DefaultMaxEntries))
	}
	if got, _, _ := Keep(kept, DefaultMaxEntries); len(got) != MaxExtras(DefaultMaxEntries) {
		t.Errorf("Keep() with the default kept %d extras, want %d", len(got), MaxExtras(DefaultMaxEntries))
	}
}

func TestKeepLeavesOutAnEntryWhoseLongestMetricIsTooLong(t *testing.T) {
	// "disk:" and this path fit, "disk.write:" and it do not: the recorder
	// leaves out the whole disk, so the hub does too.
	path := "/" + strings.Repeat("x", MaxMetricLength-len("disk:")-1)
	minute := map[string]float64{"cpu": 1, "disk:" + path: 2, "disk.read:/a": 3, "temperature:" + path: 4, "fan:" + path + "xxxxxx": 5}

	got, dropped, tooLong := Keep(minute, 10)

	want := map[string]float64{"cpu": 1, "disk.read:/a": 3}
	if !reflect.DeepEqual(got, want) || dropped || !tooLong {
		t.Errorf("Keep() = %v, %v, %v; want %v, false, true", got, dropped, tooLong, want)
	}
}

func TestKeepKeepsAtMostSoManyMetricsItDoesNotKnow(t *testing.T) {
	minute := map[string]float64{"cpu": 1}
	for kind := range 4 {
		minute[fmt.Sprintf("new%d", kind)] = 1
		for name := range 4 {
			minute[fmt.Sprintf("kind%d:%d", kind, name)] = 1
		}
	}

	got, dropped, _ := Keep(minute, 2)

	// Of the metrics and kinds of them, new0, new1, kind0 and kind1 by name,
	// the first two: kind0 and kind1, with two names each.
	want := map[string]float64{"cpu": 1, "kind0:0": 1, "kind0:1": 1, "kind1:0": 1, "kind1:1": 1}
	if !reflect.DeepEqual(got, want) || !dropped {
		t.Errorf("Keep() = %v, %v; want %v, true", got, dropped, want)
	}
}

func TestLiveAndFetchedMinutesKeepTheSameValues(t *testing.T) {
	number := func(v float64) *float64 { return &v }
	item := func(id string, history bool) metrics.ExtraItem {
		return metrics.ExtraItem{ID: id, Label: id, Unit: metrics.UnitNumber, Value: number(1), History: history}
	}
	text := metrics.ExtraItem{ID: "aa-text", Label: "Text", Unit: metrics.UnitText, Text: "hi"}
	// More than kept of everything, in no order, with groups and values that
	// keep no history among them.
	raw := metrics.Snapshot{
		Temperatures: []metrics.Temperature{{Sensor: "c", Celsius: 3}, {Sensor: "a", Celsius: 1}, {Sensor: "b", Celsius: 2}},
		Disks:        []metrics.Disk{{Path: "/mnt/b"}, {Path: "/"}, {Path: "/mnt/a"}},
		Network:      []metrics.NetworkInterface{{Name: "wlan0"}, {Name: "eth1"}, {Name: "eth0"}},
		Extras: []metrics.Extra{
			{ID: "zeta", Items: []metrics.ExtraItem{text, item("c", true), item("b", true), item("a", false), item("d", true)}},
			{ID: "texts", Items: []metrics.ExtraItem{text}},
			{ID: "live", Items: []metrics.ExtraItem{item("a", false), item("b", false)}},
			{ID: "beta", Items: []metrics.ExtraItem{item("z", true), item("y", true), item("x", true)}},
			{ID: "alpha", Items: []metrics.ExtraItem{item("one", true)}},
		},
	}
	const kept = 2
	// Live: the hub cuts the extras of a reading down, then keeps its values.
	liveSnapshot := raw
	liveSnapshot.Extras = metrics.CleanExtras(raw.Extras, kept)
	live, liveDropped, _ := values(liveSnapshot, kept)
	// Fetched: the device keeps all of its own, the hub keeps of the minute.
	deviceSnapshot := raw
	deviceSnapshot.Extras = metrics.CleanExtras(raw.Extras, DefaultMaxEntries)
	device, _, _ := values(deviceSnapshot, DefaultMaxEntries)
	fetched, fetchedDropped, _ := Keep(device, kept)

	if !reflect.DeepEqual(live, fetched) || !liveDropped || !fetchedDropped {
		t.Errorf("kept live %v (dropped %v), from a fetched minute %v (dropped %v); want the same, with some dropped", live, liveDropped, fetched, fetchedDropped)
	}
	if want := []string{"extra:alpha/one", "extra:beta/x", "extra:beta/y"}; !reflect.DeepEqual(extrasOf(live), want) {
		t.Errorf("extras kept live %v, want %v: the first groups and values by id that keep a history", extrasOf(live), want)
	}
}

// extrasOf returns the metrics of extras among kept, sorted.
func extrasOf(kept map[string]float64) []string {
	var extras []string
	for metric := range kept {
		if strings.HasPrefix(metric, MetricExtra+":") {
			extras = append(extras, metric)
		}
	}
	slices.Sort(extras)
	return extras
}
