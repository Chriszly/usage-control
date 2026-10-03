package metrics

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Chriszly/usage-control/backend/internal/version"
	"github.com/shirou/gopsutil/v4/cpu"
)

func TestCollectReadsThisMachine(t *testing.T) {
	collector, err := NewCollector(context.Background(), []string{"/"})
	if err != nil {
		t.Fatalf("NewCollector() error = %v", err)
	}
	snapshot, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if snapshot.Time.IsZero() {
		t.Error("Time is not set")
	}
	if snapshot.Version != version.Version {
		t.Errorf("Version = %q, want %q", snapshot.Version, version.Version)
	}
	if snapshot.CPU.Cores < 1 {
		t.Errorf("CPU.Cores = %d, want at least 1", snapshot.CPU.Cores)
	}
	if p := snapshot.CPU.UsagePercent; p < 0 || p > 100 {
		t.Errorf("CPU.UsagePercent = %v, want 0 to 100", p)
	}
	if snapshot.Memory.TotalBytes == 0 {
		t.Error("Memory.TotalBytes is 0")
	}
	if snapshot.Memory.UsedBytes > snapshot.Memory.TotalBytes {
		t.Errorf("Memory.UsedBytes = %d is more than TotalBytes = %d", snapshot.Memory.UsedBytes, snapshot.Memory.TotalBytes)
	}
	if snapshot.Temperatures == nil {
		t.Error("Temperatures is nil, want an empty list when no sensor is available")
	}
	if len(snapshot.Disks) != 1 || snapshot.Disks[0].Path != "/" || snapshot.Disks[0].TotalBytes == 0 {
		t.Errorf("Disks = %+v, want the usage of /", snapshot.Disks)
	}
	if snapshot.Network == nil {
		t.Error("Network is nil, want an empty list when there is no network card")
	}
	if n := len(snapshot.CPU.CoreUsagePercent); n != 0 && n != snapshot.CPU.Cores {
		t.Errorf("CPU.CoreUsagePercent has %d cores, want %d", n, snapshot.CPU.Cores)
	}
}

func TestNewCollectorRefusesUnreadableDiskPaths(t *testing.T) {
	for _, path := range []string{"relative/path", "/does/not/exist"} {
		if _, err := NewCollector(context.Background(), []string{path}); err == nil {
			t.Errorf("NewCollector(%q) error = nil, want an error", path)
		}
	}
}

func TestBusyPercent(t *testing.T) {
	previous := cpu.TimesStat{User: 100, System: 50, Idle: 800, Iowait: 50}
	tests := []struct {
		name     string
		previous cpu.TimesStat
		current  cpu.TimesStat
		want     float64
	}{
		{"since boot", cpu.TimesStat{}, previous, 15},
		{"a quarter busy", previous, cpu.TimesStat{User: 120, System: 55, Idle: 870, Iowait: 55}, 25},
		{"no time passed", previous, previous, 0},
		{"counters reset", previous, cpu.TimesStat{User: 1, Idle: 1}, 0},
	}
	for _, tt := range tests {
		if got := busyPercent(tt.previous, tt.current); got != tt.want {
			t.Errorf("%s: busyPercent() = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestSumTimesAddsUpEachCore(t *testing.T) {
	got := sumTimes([]cpu.TimesStat{{User: 1, Idle: 10, Iowait: 2}, {User: 3, System: 4, Idle: 20}})
	want := cpu.TimesStat{User: 4, System: 4, Idle: 30, Iowait: 2}
	if got != want {
		t.Errorf("sumTimes() = %+v, want %+v", got, want)
	}
}

func TestReadClockMHzTakesTheFastestGroupOfCores(t *testing.T) {
	dir := t.TempDir()
	little := filepath.Join(dir, "policy0")
	big := filepath.Join(dir, "policy4")
	for path, kHz := range map[string]string{little: "1800000\n", big: "2400000\n"} {
		if err := os.WriteFile(path, []byte(kHz), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := readClockMHz([]string{little, big, filepath.Join(dir, "missing")}); got != 2400 {
		t.Errorf("readClockMHz() = %v, want 2400", got)
	}
	if got := readClockMHz(nil); got != 0 {
		t.Errorf("readClockMHz(nil) = %v, want 0", got)
	}
}

func TestWaitPercents(t *testing.T) {
	previous := cpu.TimesStat{User: 100, Idle: 800, Iowait: 50, Steal: 50}
	current := cpu.TimesStat{User: 130, Idle: 820, Iowait: 70, Steal: 80}
	ioWait, steal := waitPercents(previous, current)
	if ioWait != 20 || steal != 30 {
		t.Errorf("waitPercents() = %v, %v, want 20, 30", ioWait, steal)
	}
}

func TestParseLoadavg(t *testing.T) {
	avg, running, ok := parseLoadavg("0.52 0.40 0.31 2/213 12345\n")
	want := &LoadAverage{One: 0.52, Five: 0.4, Fifteen: 0.31}
	if !ok || running != 2 || *avg != *want {
		t.Errorf("parseLoadavg() = %+v, %d, %v, want %+v, 2, true", avg, running, ok, want)
	}
	if _, _, ok := parseLoadavg("0.52 0.40"); ok {
		t.Error("parseLoadavg() of a short line = true, want false")
	}
}

func TestCountProcessesCountsNumberedDirectories(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"1", "42", "1337", "self", "net", "sys"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := countProcesses(dir); err != nil || got != 3 {
		t.Errorf("countProcesses() = %d, %v, want 3", got, err)
	}
}
