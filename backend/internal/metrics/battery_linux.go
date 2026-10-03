package metrics

import (
	"path/filepath"
	"strings"
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
		})
	}
	return combineBatteries(readings)
}

// readText reads a short /sys file, or returns "" when it cannot be read.
func readText(path string) string {
	text, err := readFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(text))
}
