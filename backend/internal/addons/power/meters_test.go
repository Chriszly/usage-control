package power

import (
	"testing"
)

func TestMeterReadingsNameRAPLAsOnLinux(t *testing.T) {
	got := meterReadings(map[string]float64{
		"RAPL_Package0_DRAM": 1500,
		"_Total":             99000,
		"EMI_Display":        2250,
		"RAPL_Package0_PP0":  8000,
		"RAPL_Package0_PKG":  12500,
		"RAPL_Package1_PKG":  10000,
		"RAPL_Package0_PP1":  500,
	})

	want := []struct {
		id, label string
		watts     float64
	}{
		{"rapl-0-package-0", "CPU package 0", 12.5},
		{"rapl-0-core", "CPU cores", 8},
		{"rapl-0-uncore", "CPU uncore", 0.5},
		{"rapl-0-dram", "Memory", 1.5},
		{"rapl-1-package-1", "CPU package 1", 10},
		{"meter-emi-display", "EMI Display", 2.25},
	}
	if len(got) != len(want) {
		t.Fatalf("meterReadings() = %+v, want %d readings", got, len(want))
	}
	for i, w := range want {
		if got[i].ID != w.id || got[i].Label != w.label || got[i].Watts != w.watts {
			t.Errorf("reading %d = %+v, want %s %q %v W", i, got[i], w.id, w.label, w.watts)
		}
	}
	if got[3].Labels["de"] != "Arbeitsspeicher" {
		t.Errorf("DRAM labels = %v, want the translations of Linux", got[3].Labels)
	}
}

func TestBatteryReadingOnlyWhileDischarging(t *testing.T) {
	got, ok := batteryReading(0, batteryDischarging, -7350)
	if !ok || got.ID != "battery-0" || got.Label != "Battery discharge" || got.Watts != 7.35 {
		t.Errorf("batteryReading() = %+v, %v; want battery-0 at 7.35 W", got, ok)
	}
	second, _ := batteryReading(1, batteryDischarging, -1000)
	if second.Label != "Battery discharge 2" || second.Labels["de"] != "Akku-Entladung 2" {
		t.Errorf("second battery = %+v, want numbered labels", second)
	}
	const charging = 0x00000004
	for _, c := range []struct {
		state uint32
		rate  int32
	}{{charging, 20000}, {0, 0}, {batteryDischarging, batteryUnknownRate}} {
		if r, ok := batteryReading(0, c.state, c.rate); ok {
			t.Errorf("batteryReading(%#x, %d) = %+v, want none", c.state, c.rate, r)
		}
	}
}
