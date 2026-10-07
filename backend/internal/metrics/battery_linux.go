package metrics

import (
	"math"
	"path/filepath"
	"strconv"

	"github.com/Chriszly/usage-control/backend/internal/sysfile"
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
		if sysfile.Text(filepath.Join(dir, "type")) == "Battery" && sysfile.Text(filepath.Join(dir, "scope")) != "Device" {
			r.dirs = append(r.dirs, dir)
		}
	}
	return r
}

func (r *batteryReader) read() *Battery {
	readings := make([]supplyReading, 0, len(r.dirs))
	for _, dir := range r.dirs {
		capacity, ok := sysfile.Uint(filepath.Join(dir, "capacity"))
		if !ok {
			continue
		}
		readings = append(readings, supplyReading{
			percent: float64(min(capacity, 100)),
			status:  sysfile.Text(filepath.Join(dir, "status")),
			watts:   readWatts(dir),
			health:  readHealth(dir),
		})
	}
	return combineBatteries(readings)
}

// readWatts returns how much power flows into or out of a battery. Batteries
// report it as power_now in µW, or as current_now in µA and voltage_now in µV;
// nil when it reports neither. Some fuel gauges, as on battery HATs, report
// the power or current as negative while the battery discharges.
func readWatts(dir string) *float64 {
	if microwatts, ok := readMagnitude(filepath.Join(dir, "power_now")); ok {
		watts := microwatts / 1e6
		return &watts
	}
	microamps, currentOK := readMagnitude(filepath.Join(dir, "current_now"))
	microvolts, voltageOK := sysfile.Uint(filepath.Join(dir, "voltage_now"))
	if !currentOK || !voltageOK {
		return nil
	}
	watts := microamps * float64(microvolts) / 1e12
	return &watts
}

// readMagnitude reads a file that holds one whole number, which may be
// negative, and returns its size.
func readMagnitude(path string) (float64, bool) {
	n, err := strconv.ParseInt(sysfile.Text(path), 10, 64)
	return math.Abs(float64(n)), err == nil
}

// readHealth returns how much a battery holds when full compared to when it
// was new, in percent, from its energy (µWh) or its charge (µAh); nil when it
// reports neither.
func readHealth(dir string) *float64 {
	for _, kind := range []string{"energy", "charge"} {
		full, fullOK := sysfile.Uint(filepath.Join(dir, kind+"_full"))
		design, designOK := sysfile.Uint(filepath.Join(dir, kind+"_full_design"))
		if fullOK && designOK && design > 0 {
			health := min(100, float64(full)/float64(design)*100)
			return &health
		}
	}
	return nil
}
