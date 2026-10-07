package power

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/addons"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRAPLMeasuresPowerSinceThePreviousRead(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"intel-rapl:0/name":                  "package-0",
		"intel-rapl:0/energy_uj":             "1000000",
		"intel-rapl:0/max_energy_range_uj":   "262143328850",
		"intel-rapl:0:2/name":                "dram",
		"intel-rapl:0:2/energy_uj":           "262143000000",
		"intel-rapl:0:2/max_energy_range_uj": "262143328850",
		"intel-rapl-mmio:0/name":             "package-0",
		"intel-rapl-mmio:0/energy_uj":        "5",
	})
	r := newRAPL(dir)
	start := time.Now()

	if got := r.read(start); len(got) != 0 {
		t.Fatalf("first read() = %v, want nothing yet", got)
	}
	writeFiles(t, dir, map[string]string{
		"intel-rapl:0/energy_uj":   "51000000",
		"intel-rapl:0:2/energy_uj": "4671150", // wrapped: 328850 + 4671150 = 5 J
	})
	got := r.read(start.Add(2 * time.Second))

	if len(got) != 2 {
		t.Fatalf("read() = %+v, want package and memory", got)
	}
	if got[0].ID != "rapl-0-package-0" || got[0].Label != "CPU package 0" || got[0].Watts != 25 {
		t.Errorf("package = %+v, want 25 W as CPU package 0", got[0])
	}
	if got[1].ID != "rapl-0-2-dram" || got[1].Label != "Memory" || got[1].Watts != 2.5 {
		t.Errorf("memory = %+v, want 2.5 W after the counter wrapped", got[1])
	}
}

func TestSleptComparesTheWallClock(t *testing.T) {
	if slept(5*time.Second, 5*time.Second) {
		t.Error("slept() is true for two clocks that agree")
	}
	if slept(5*time.Second, 5*time.Second+300*time.Millisecond) {
		t.Error("slept() is true for a wall clock a little ahead")
	}
	if !slept(5*time.Second, 8*time.Hour) {
		t.Error("slept() is false for eight hours of sleep, which would read as a huge spike")
	}
	if slept(5*time.Second, -time.Hour) {
		t.Error("slept() is true for a wall clock set back, which the monotonic clock is not fooled by")
	}
}

func TestHwmonPrefersTheAverage(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"hwmon3/name":           "amdgpu",
		"hwmon3/power1_average": "42500000",
		"hwmon3/power1_input":   "50000000",
		"hwmon4/name":           "ina219",
		"hwmon4/power1_input":   "3200000",
		"hwmon4/power1_label":   "board",
	})
	// The folder's number can change at a reboot; the device it links to does not.
	if err := os.Symlink("../../devices/pci0000:00/0000:03:00.0", filepath.Join(dir, "hwmon3", "device")); err != nil {
		t.Fatal(err)
	}

	got := readHwmon(dir)

	want := []Reading{
		{ID: "hwmon-amdgpu-0000-03-00-0-power1", Label: "amdgpu", Watts: 42.5},
		{ID: "hwmon-ina219-power1", Label: "ina219 board", Watts: 3.2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("readHwmon() = %+v, want %+v", got, want)
	}
}

func TestParsePMICAddsUpTheRails(t *testing.T) {
	out := `     3V7_WL_SW_A current(0)=0.10000000A
     VDD_CORE_A current(7)=2.00000000A
     3V7_WL_SW_V volt(8)=3.70000000V
     VDD_CORE_V volt(15)=0.80000000V
     EXT5V_V volt(24)=5.10000000V
     BATT_V volt(25)=0.00000000V
`
	watts, ok := parsePMIC(out)
	if !ok || watts < 1.969 || watts > 1.971 {
		t.Errorf("parsePMIC() = %v, %v; want 0.37 + 1.6 = 1.97 W", watts, ok)
	}
	if _, ok := parsePMIC("error=1 error_msg=\"Command not registered\"\n"); ok {
		t.Error("parsePMIC() of a Pi without the chip is ok, want false")
	}
}

func TestParseNvidiaLeavesOutGPUsWithoutPower(t *testing.T) {
	got := parseNvidia("0, GPU-1a2b3c4d-0000-0000-0000-000000000000, NVIDIA GeForce RTX 5060 Ti, 18.42\n" +
		"1, GPU-5e6f7a8b-0000-0000-0000-000000000000, NVIDIA T400, [N/A]\n")

	// With two GPUs, each is named by its UUID, which stays with the card.
	want := []Reading{{ID: "nvidia-1a2b3c4d", Label: "NVIDIA GeForce RTX 5060 Ti", Watts: 18.42}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseNvidia() = %+v, want %+v", got, want)
	}
}

func TestLastReadingsAsksAProgramEveryInterval(t *testing.T) {
	calls := 0
	read := func() []Reading {
		calls++
		return []Reading{{ID: "nvidia-0", Watts: float64(calls)}}
	}
	var last lastReadings
	start := time.Now()

	for _, after := range []time.Duration{0, 5 * time.Second, addons.ProgramInterval - time.Second} {
		if got := last.get(start.Add(after), read); got[0].Watts != 1 {
			t.Errorf("get() after %v = %v, want the first answer", after, got)
		}
	}
	if got := last.get(start.Add(addons.ProgramInterval), read); got[0].Watts != 2 || calls != 2 {
		t.Errorf("get() after the interval = %v after %d calls, want a new answer", got, calls)
	}
}

func TestParseNvidiaKeepsTheIDOfGPU0WhileTheOtherFails(t *testing.T) {
	got := parseNvidia("0, GPU-1a2b3c4d-0000-0000-0000-000000000000, NVIDIA GeForce RTX 5060 Ti, 18.42\n" +
		"Unable to determine the device handle for GPU0000:02:00.0: Unknown Error\n" +
		"[Unknown Error], [Unknown Error], [Unknown Error], [Unknown Error]\n")

	want := []Reading{{ID: "nvidia-1a2b3c4d", Label: "NVIDIA GeForce RTX 5060 Ti", Watts: 18.42}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseNvidia() = %+v, want %+v, as while both GPUs work", got, want)
	}
}

func TestParseNvidiaKeepsTheIDOfTheOnlyGPU(t *testing.T) {
	got := parseNvidia("0, GPU-1a2b3c4d-0000-0000-0000-000000000000, Odd, Name, 18.42\n")

	want := []Reading{{ID: "nvidia-0", Label: "Odd, Name", Watts: 18.42}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseNvidia() = %+v, want %+v", got, want)
	}
}

func TestExtrasKeepTheHistoryOfEveryReading(t *testing.T) {
	got := Extras([]Reading{{ID: "nvidia-0", Label: "GPU", Watts: 18}})

	if len(got) != 1 || got[0].ID != "power" || len(got[0].Items) != 1 {
		t.Fatalf("Extras() = %+v, want one power group with one value", got)
	}
	item := got[0].Items[0]
	if item.Unit != "watts" || !item.History || *item.Value != 18 {
		t.Errorf("item = %+v, want 18 W with history", item)
	}
	if Extras(nil) != nil {
		t.Error("Extras(nil) is not nil, want no group without readings")
	}
}

func TestExtrasNumberLabelsThatOccurTwice(t *testing.T) {
	gpu := map[string]string{"de": "Grafikkarte"}
	got := Extras([]Reading{
		{ID: "nvidia-0", Label: "GPU", Labels: gpu, Watts: 18},
		{ID: "nvidia-1", Label: "GPU", Labels: gpu, Watts: 20},
		{ID: "pmic", Label: "Total", Watts: 5},
	})[0].Items

	if got[0].Label != "GPU 1" || got[1].Label != "GPU 2" || got[2].Label != "Total" {
		t.Errorf("labels = %q, %q, %q, want GPU 1, GPU 2 and Total", got[0].Label, got[1].Label, got[2].Label)
	}
	if got[0].Labels["de"] != "Grafikkarte 1" || got[1].Labels["de"] != "Grafikkarte 2" {
		t.Errorf("German labels = %q and %q, want them numbered too", got[0].Labels["de"], got[1].Labels["de"])
	}
	if gpu["de"] != "Grafikkarte" {
		t.Errorf("the reading's own labels changed to %q", gpu["de"])
	}
}

func TestIDOf(t *testing.T) {
	if got := idOf("rapl", "0:2", "dram"); got != "rapl-0-2-dram" {
		t.Errorf("idOf() = %q", got)
	}
	// Two long names that only differ at the end must not become one id.
	a := idOf("meter", "Intel Energy Metering Interface Display Panel 1")
	b := idOf("meter", "Intel Energy Metering Interface Display Panel 2")
	if a == b || len(a) > 40 || len(b) > 40 {
		t.Errorf("idOf() = %q and %q, want two different ids of at most 40 characters", a, b)
	}
}
