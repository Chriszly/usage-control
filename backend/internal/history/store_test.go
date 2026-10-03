package history

import (
	"context"
	"path/filepath"
	"reflect"
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

func TestValuesNamesEachDiskSensorInterfaceAndGPU(t *testing.T) {
	read, write := 4096.0, 512.0
	snapshot := metrics.Snapshot{
		CPU:          metrics.CPU{UsagePercent: 12, CoreUsagePercent: []float64{10, 14}, ClockMHz: 1500},
		Memory:       metrics.Memory{UsedPercent: 34, Swap: &metrics.Swap{UsedPercent: 5}},
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
	if got := values(snapshot); !reflect.DeepEqual(got, want) {
		t.Errorf("values() = %v, want %v", got, want)
	}
}
