package history

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestRangeAveragesEachStep(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	start := time.Unix(1_800_000_000, 0) // a multiple of the 10 minute step below
	for i, cpu := range []float64{10, 20, 30, 40} {
		at := start.Add(time.Duration(i) * 5 * time.Minute)
		if err := store.Add(ctx, LocalDevice, at, map[string]float64{MetricCPU: cpu, MetricMemory: 50}); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}
	if err := store.Add(ctx, "other", start, map[string]float64{MetricCPU: 99}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	got, err := store.Range(ctx, LocalDevice, start, start.Add(20*time.Minute), 10*time.Minute)
	if err != nil {
		t.Fatalf("Range() error = %v", err)
	}

	want := []Series{
		{Metric: MetricCPU, Points: []Point{{start.Unix(), 15}, {start.Unix() + 600, 35}}},
		{Metric: MetricMemory, Points: []Point{{start.Unix(), 50}, {start.Unix() + 600, 50}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Range() = %+v, want %+v", got, want)
	}
}

func TestRangeReadsStepsOfAnHourAndMoreFromTheHourlyAverages(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	hour := time.Unix(1_800_000_000, 0) // the start of an hour
	for _, sample := range []struct {
		at  time.Duration
		cpu float64
	}{{time.Minute, 10}, {2 * time.Minute, 20}, {time.Hour + time.Minute, 60}} {
		if err := store.Add(ctx, LocalDevice, hour.Add(sample.at), map[string]float64{MetricCPU: sample.cpu}); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}
	if _, err := store.Add(ctx, "other", hour, map[string]float64{MetricCPU: 99}), error(nil); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	hourly, err := store.Range(ctx, LocalDevice, hour, hour.Add(2*time.Hour), time.Hour)
	if err != nil {
		t.Fatalf("Range(1 h) error = %v", err)
	}
	want := []Series{{Metric: MetricCPU, Points: []Point{{hour.Unix(), 15}, {hour.Unix() + 3600, 60}}}}
	if !reflect.DeepEqual(hourly, want) {
		t.Errorf("Range(1 h) = %+v, want %+v", hourly, want)
	}
	// Two hours averaged together weigh each hour by its number of values.
	twoHourly, err := store.Range(ctx, LocalDevice, hour, hour.Add(2*time.Hour), 2*time.Hour)
	if err != nil {
		t.Fatalf("Range(2 h) error = %v", err)
	}
	want = []Series{{Metric: MetricCPU, Points: []Point{{hour.Unix(), 30}}}}
	if !reflect.DeepEqual(twoHourly, want) {
		t.Errorf("Range(2 h) = %+v, want %+v", twoHourly, want)
	}

	// The hourly averages are read, not the values: they answer without them.
	if _, err := store.DB().ExecContext(ctx, `DELETE FROM samples`); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Range(ctx, LocalDevice, hour, hour.Add(2*time.Hour), 2*time.Hour); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Range(2 h) without the values = %+v, %v; want %+v from the hourly averages", got, err, want)
	}
	if got, err := store.Range(ctx, LocalDevice, hour, hour.Add(2*time.Hour), time.Minute); err != nil || len(got) != 0 {
		t.Errorf("Range(1 min) without the values = %+v, %v; want it empty", got, err)
	}
}

func TestRangeOfHoursKeepsTheHourItStartsIn(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	hour := time.Unix(1_800_000_000, 0) // the start of an hour
	for _, at := range []time.Duration{10 * time.Minute, 50 * time.Minute, time.Hour + time.Minute} {
		if err := store.Add(ctx, LocalDevice, hour.Add(at), map[string]float64{MetricCPU: 10}); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}

	// From the middle of the first hour, as a long range starts at any time.
	got, err := store.Range(ctx, LocalDevice, hour.Add(30*time.Minute), hour.Add(2*time.Hour), time.Hour)

	want := []Series{{Metric: MetricCPU, Points: []Point{{hour.Unix(), 10}, {hour.Unix() + 3600, 10}}}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Range() = %+v, %v; want %+v, with the hour it starts in", got, err, want)
	}
}

func TestRangeOfLongerStepsKeepsTheWholeStepItStartsIn(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	start := time.Unix(1_800_000_000/7200*7200, 0) // the start of a 2 hour step
	for i, at := range []time.Duration{10 * time.Minute, time.Hour + 10*time.Minute, 2*time.Hour + 10*time.Minute} {
		if err := store.Add(ctx, LocalDevice, start.Add(at), map[string]float64{MetricCPU: float64(10 * (i + 1))}); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}

	// From either hour of the first step: the same answer, as the cache gives.
	for _, from := range []time.Time{start.Add(30 * time.Minute), start.Add(90 * time.Minute)} {
		got, err := store.Range(ctx, LocalDevice, from, start.Add(4*time.Hour), 2*time.Hour)
		want := []Series{{Metric: MetricCPU, Points: []Point{{start.Unix(), 15}, {start.Unix() + 7200, 30}}}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("Range(from %v) = %+v, %v; want %+v, with the whole first step", from, got, err, want)
		}
	}
}

func TestRangeIsNotCachedAcrossADeleteOfTheDevice(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	from, to := time.Unix(1_800_000_000, 0), time.Unix(1_800_000_000, 0).Add(20*24*time.Hour)
	if err := store.Add(ctx, "office-pc", from.Add(time.Hour), map[string]float64{MetricCPU: 10}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	// A range read while the device is deleted, which finishes after it.
	_, _, forgotten := store.cache.get("office-pc", from, to, 2*time.Hour)
	series, err := store.Range(ctx, "office-pc", from, to, 2*time.Hour)
	if err != nil || len(series) == 0 {
		t.Fatalf("Range() = %+v, %v; want the value", series, err)
	}
	if err := store.DeleteDevice(ctx, "office-pc"); err != nil {
		t.Fatalf("DeleteDevice() error = %v", err)
	}
	store.cache.put("office-pc", from, to, 2*time.Hour, series, forgotten)

	if got, err := store.cachedRange(ctx, "office-pc", from, to, 2*time.Hour); err != nil || len(got) != 0 {
		t.Errorf("cachedRange() after the device was deleted = %+v, %v; want nothing", got, err)
	}
}

func TestRangeWithoutValuesIsEmpty(t *testing.T) {
	got, err := openTestStore(t).Range(context.Background(), LocalDevice, time.Unix(0, 0), time.Now(), time.Minute)
	if err != nil {
		t.Fatalf("Range() error = %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("Range() = %#v, want an empty list", got)
	}
}

func TestDeleteBeforeKeepsNewerValues(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	old, recent := time.Unix(1_000, 0), time.Unix(2_000, 0)
	for _, at := range []time.Time{old, recent} {
		if err := store.Add(ctx, LocalDevice, at, map[string]float64{MetricCPU: 1}); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}

	deleted, err := store.DeleteBefore(ctx, recent)
	if err != nil {
		t.Fatalf("DeleteBefore() error = %v", err)
	}

	if deleted != 1 {
		t.Errorf("DeleteBefore() deleted %d values, want 1", deleted)
	}
	got, _ := store.Range(ctx, LocalDevice, old, recent.Add(time.Second), time.Second)
	if len(got) != 1 || len(got[0].Points) != 1 || got[0].Points[0].Time != recent.Unix() {
		t.Errorf("after DeleteBefore, Range() = %+v, want only the recent value", got)
	}
}

func TestDeleteBeforeKeepsTheAveragesOfHoursWithValues(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	hour := time.Unix(1_800_000_000, 0)
	for _, at := range []time.Duration{time.Minute, time.Hour + time.Minute, time.Hour + 2*time.Minute} {
		if err := store.Add(ctx, LocalDevice, hour.Add(at), map[string]float64{MetricCPU: 1}); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}

	// Deleting into the second hour removes the first hour's average and keeps
	// the second one, which still has a value.
	if _, err := store.DeleteBefore(ctx, hour.Add(time.Hour+90*time.Second)); err != nil {
		t.Fatalf("DeleteBefore() error = %v", err)
	}

	got, err := store.Range(ctx, LocalDevice, hour, hour.Add(2*time.Hour), time.Hour)
	if err != nil {
		t.Fatalf("Range() error = %v", err)
	}
	want := []Series{{Metric: MetricCPU, Points: []Point{{hour.Unix() + 3600, 1}}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("after DeleteBefore, Range(1 h) = %+v, want %+v", got, want)
	}
}

func TestDeleteDeviceDeletesItsValuesAndHourlyAverages(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	hour := time.Unix(1_800_000_000, 0)
	for _, device := range []string{LocalDevice, "other"} {
		if err := store.Add(ctx, device, hour.Add(time.Minute), map[string]float64{MetricCPU: 1}); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}

	if err := store.DeleteDevice(ctx, "other"); err != nil {
		t.Fatalf("DeleteDevice() error = %v", err)
	}

	for _, step := range []time.Duration{time.Minute, time.Hour} {
		if got, err := store.Range(ctx, "other", hour, hour.Add(time.Hour), step); err != nil || len(got) != 0 {
			t.Errorf("Range(other, %v) = %+v, %v; want it empty", step, got, err)
		}
		if got, err := store.Range(ctx, LocalDevice, hour, hour.Add(time.Hour), step); err != nil || len(got) != 1 {
			t.Errorf("Range(local, %v) = %+v, %v; want this device's value kept", step, got, err)
		}
	}
}

// addMany stores count values of device, one minute apart from start on, a
// few metrics per minute, in one go.
func addMany(t *testing.T, store *Store, device string, start time.Time, count int) {
	t.Helper()
	metricNames := []string{MetricCPU, MetricMemory, MetricSwap, MetricBattery}
	var minutes []Minute
	for i := 0; i < count; i += len(metricNames) {
		values := map[string]float64{}
		for _, metric := range metricNames[:min(len(metricNames), count-i)] {
			values[metric] = 1
		}
		minutes = append(minutes, Minute{Time: start.Unix() + int64(i/len(metricNames))*60, Values: values})
	}
	if err := store.AddMinutes(context.Background(), device, minutes); err != nil {
		t.Fatalf("AddMinutes() error = %v", err)
	}
}

func countRows(t *testing.T, store *Store, table, device string) int {
	t.Helper()
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE device = ?`, device).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func TestDeleteBeforeDeletesMoreThanAChunk(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	start := time.Unix(1_800_000_000, 0)
	old := 2*deleteChunkRows + 10
	addMany(t, store, LocalDevice, start, old)
	cutoff := start.Add(time.Duration(old/4+1) * time.Minute)
	addMany(t, store, LocalDevice, cutoff, 8)

	deleted, err := store.DeleteBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("DeleteBefore() error = %v", err)
	}

	if deleted != int64(old) {
		t.Errorf("DeleteBefore() deleted %d values, want %d", deleted, old)
	}
	if got := countRows(t, store, "samples", LocalDevice); got != 8 {
		t.Errorf("after DeleteBefore, %d values are left, want the 8 newer ones", got)
	}
	var older int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM samples_hourly WHERE time < ?`, cutoff.Truncate(time.Hour).Unix()).Scan(&older); err != nil || older != 0 {
		t.Errorf("hourly averages before the cutoff = %d, %v; want none", older, err)
	}
}

func TestDeleteDeviceDeletesMoreThanAChunk(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	start := time.Unix(1_800_000_000, 0)
	addMany(t, store, "other", start, deleteChunkRows+10)
	addMany(t, store, LocalDevice, start, 8)

	if err := store.DeleteDevice(ctx, "other"); err != nil {
		t.Fatalf("DeleteDevice() error = %v", err)
	}

	for _, table := range []string{"samples", "samples_hourly"} {
		if got := countRows(t, store, table, "other"); got != 0 {
			t.Errorf("%s of the deleted device = %d rows, want none", table, got)
		}
	}
	if got := countRows(t, store, "samples", LocalDevice); got != 8 {
		t.Errorf("values of the other device = %d, want its 8 kept", got)
	}
}

func TestValuesNamesEachDiskSensorInterfaceAndGPU(t *testing.T) {
	read, write := 4096.0, 512.0
	snapshot := metrics.Snapshot{
		CPU:          metrics.CPU{UsagePercent: 12, CoreUsagePercent: []float64{10, 14}, ClockMHz: 1500},
		Memory:       metrics.Memory{UsedPercent: 34, Swap: &metrics.Swap{UsedPercent: 5}},
		Battery:      &metrics.Battery{Percent: 87, PluggedIn: true},
		Temperatures: []metrics.Temperature{{Sensor: "cpu_thermal", Celsius: 48}},
		Disks: []metrics.Disk{
			{Path: "/", UsedPercent: 20, ReadBytesPerSecond: &read, WriteBytesPerSecond: &write},
			{Path: "/mnt/usb", UsedPercent: 56},
		},
		Network: []metrics.NetworkInterface{{Name: "eth0", ReceiveBytesPerSecond: 1000, SendBytesPerSecond: 200}},
		GPUs: []metrics.GPU{
			{Name: "AMD GPU", UsagePercent: 78, MemoryTotalBytes: 400, MemoryUsedBytes: 100},
			{Name: "VideoCore GPU", UsagePercent: 9},
		},
	}

	want := map[string]float64{
		"cpu":                     12,
		"memory":                  34,
		"swap":                    5,
		"battery":                 87,
		"temperature:cpu_thermal": 48,
		"disk:/":                  20,
		"disk.read:/":             4096,
		"disk.write:/":            512,
		"disk:/mnt/usb":           56,
		"network.receive:eth0":    1000,
		"network.send:eth0":       200,
		"gpu:AMD GPU":             78,
		"gpu.memory:AMD GPU":      25,
		"gpu:VideoCore GPU":       9,
	}
	if got, dropped, _ := values(snapshot, DefaultMaxEntries); !reflect.DeepEqual(got, want) || dropped {
		t.Errorf("values() = %v, %v; want %v and nothing dropped", got, dropped, want)
	}
}

func TestValuesKeepsTheFirstEntriesOfEachList(t *testing.T) {
	snapshot := metrics.Snapshot{
		// Kept by name, not in the order the device lists them.
		Temperatures: []metrics.Temperature{{Sensor: "c", Celsius: 3}, {Sensor: "b", Celsius: 2}, {Sensor: "a", Celsius: 1}},
		Disks:        []metrics.Disk{{Path: "/", UsedPercent: 20}, {Path: "/mnt/a", UsedPercent: 30}, {Path: "/mnt/b", UsedPercent: 40}},
		Network:      []metrics.NetworkInterface{{Name: "eth0"}, {Name: "eth1"}, {Name: "eth2"}},
		GPUs:         []metrics.GPU{{Name: "g0", UsagePercent: 1}, {Name: "g1", UsagePercent: 2}, {Name: "g2", UsagePercent: 3}},
	}

	got, dropped, _ := values(snapshot, 2)

	want := map[string]float64{
		"cpu": 0, "memory": 0,
		"temperature:a": 1, "temperature:b": 2,
		"disk:/": 20, "disk:/mnt/a": 30,
		"network.receive:eth0": 0, "network.send:eth0": 0, "network.receive:eth1": 0, "network.send:eth1": 0,
		"gpu:g0": 1, "gpu:g1": 2,
	}
	if !reflect.DeepEqual(got, want) || !dropped {
		t.Errorf("values(2 entries) = %v, %v; want %v with entries dropped", got, dropped, want)
	}
	if _, dropped, _ := values(snapshot, 3); dropped {
		t.Error("values(3 entries) dropped entries, want none with three of each")
	}
}

func TestValuesLeavesOutEntriesWithTooLongNames(t *testing.T) {
	long := strings.Repeat("x", MaxMetricLength)
	// The longest name whose longest metric, network.receive:<name>, fits.
	fitting := strings.Repeat("n", MaxMetricLength-len(MetricNetworkReceive+":"))
	snapshot := metrics.Snapshot{
		Temperatures: []metrics.Temperature{{Sensor: long, Celsius: 1}, {Sensor: "cpu", Celsius: 2}},
		Disks:        []metrics.Disk{{Path: long, UsedPercent: 20}, {Path: "/", UsedPercent: 30}},
		Network:      []metrics.NetworkInterface{{Name: fitting + "n"}, {Name: fitting}},
		GPUs:         []metrics.GPU{{Name: long, UsagePercent: 1}},
	}

	got, _, tooLong := values(snapshot, 1)

	want := map[string]float64{
		"cpu": 0, "memory": 0, "temperature:cpu": 2, "disk:/": 30,
		"network.receive:" + fitting: 0, "network.send:" + fitting: 0,
	}
	if !reflect.DeepEqual(got, want) || !tooLong {
		t.Errorf("values() = %v, %v; want %v with names left out", got, tooLong, want)
	}
}

func TestValuesKeepsAtMostSoManyValuesOfExtras(t *testing.T) {
	const maxEntries = 2
	var extras []metrics.Extra
	for g := range 3 {
		group := metrics.Extra{ID: fmt.Sprintf("g%d", g), Title: "Group"}
		for i := range 7 {
			group.Items = append(group.Items, metrics.ExtraItem{ID: fmt.Sprintf("v%d", i), Label: "Value", Unit: metrics.UnitNumber, Value: number(1), History: true})
		}
		// Listed last to first: kept by metric name, not in the device's order.
		extras = append([]metrics.Extra{group}, extras...)
	}

	got, dropped, _ := values(metrics.Snapshot{Extras: extras}, maxEntries)

	stored := 0
	for metric := range got {
		if strings.HasPrefix(metric, MetricExtra+":") {
			stored++
		}
	}
	if stored != extrasPerEntry*maxEntries || !dropped {
		t.Errorf("values() kept %d values of extras, dropped = %v; want %d with some dropped", stored, dropped, extrasPerEntry*maxEntries)
	}
	_, last := got["extra:g2/v1"]
	_, past := got["extra:g2/v2"]
	if !last || past {
		t.Errorf("values() = %v, want the first values by name kept, up to extra:g2/v1", got)
	}
	if info := extraInfo(extras, maxEntries); len(info) != stored {
		t.Errorf("extraInfo() describes %d values, want the %d stored", len(info), stored)
	}
}
