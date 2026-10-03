package metrics

import (
	"path/filepath"
)

// batteryReader reads the batteries Linux lists in /sys/class/power_supply.
// The batteries of a mouse or keyboard (scope "Device") are left out.
type batteryReader struct {
	dirs []string
}

// newBatteryReader looks up the machine's batteries once; a battery is part
// of the machine, so the list does not change while it runs.
func newBatteryReader() *batteryReader {
	supplies, _ := filepath.Glob(filepath.Join(hostPath("HOST_SYS", "/sys"), "class", "power_supply", "*"))
	r := &batteryReader{}
	for _, dir := range supplies {
		if readText(filepath.Join(dir, "type")) == "Battery" && readText(filepath.Join(dir, "scope")) != "Device" {
			r.dirs = append(r.dirs, dir)
		}
	}
	return r
}

func (r *batteryReader) read() *Battery {
	readings := make([]supplyReading, 0, len(r.dirs))
	for _, dir := range r.dirs {
		capacity, err := readUint(filepath.Join(dir, "capacity"))
		if err != nil {
			continue
		}
		readings = append(readings, supplyReading{
			percent: float64(min(capacity, 100)),
			status:  readText(filepath.Join(dir, "status")),
			watts:   readWatts(dir),
			health:  readHealth(dir),
		})
	}
	return combineBatteries(readings)
}

// readWatts returns how much power flows into or out of a battery. Batteries
// report it as power_now in µW, or as current_now in µA and voltage_now in µV;
// nil when it reports neither.
func readWatts(dir string) *float64 {
	if microwatts, err := readUint(filepath.Join(dir, "power_now")); err == nil {
		watts := float64(microwatts) / 1e6
		return &watts
	}
	microamps, currentErr := readUint(filepath.Join(dir, "current_now"))
	microvolts, voltageErr := readUint(filepath.Join(dir, "voltage_now"))
	if currentErr != nil || voltageErr != nil {
		return nil
	}
	watts := float64(microamps) * float64(microvolts) / 1e12
	return &watts
}

// readHealth returns how much a battery holds when full compared to when it
// was new, in percent, from its energy (µWh) or its charge (µAh); nil when it
// reports neither.
func readHealth(dir string) *float64 {
	for _, kind := range []string{"energy", "charge"} {
		full, fullErr := readUint(filepath.Join(dir, kind+"_full"))
		design, designErr := readUint(filepath.Join(dir, kind+"_full_design"))
		if fullErr == nil && designErr == nil && design > 0 {
			health := min(100, float64(full)/float64(design)*100)
			return &health
		}
	}
	return nil
}
