package power

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
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

	got := readHwmon(dir)

	want := []Reading{
		{ID: "hwmon-hwmon3-power1", Label: "amdgpu", Watts: 42.5},
		{ID: "hwmon-hwmon4-power1", Label: "ina219 board", Watts: 3.2},
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
	got := parseNvidia("0, NVIDIA GeForce RTX 5060 Ti, 18.42\n1, NVIDIA T400, [N/A]\n")

	want := []Reading{{ID: "nvidia-0", Label: "NVIDIA GeForce RTX 5060 Ti", Watts: 18.42}}
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

func TestIDOf(t *testing.T) {
	if got := idOf("rapl", "0:2", "dram"); got != "rapl-0-2-dram" {
		t.Errorf("idOf() = %q", got)
	}
}
