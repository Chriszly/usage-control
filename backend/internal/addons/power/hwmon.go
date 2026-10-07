package power

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
	"github.com/Chriszly/usage-control/backend/internal/sysfile"
)

// readHwmon reads the power sensors the kernel lists under
// /sys/class/hwmon, such as an AMD GPU's: power*_average, else
// power*_input, in microwatts. A device that sleeps, such as a laptop's
// second GPU, is reported at 0 W without reading its sensor, which would wake
// it (see metrics.HwmonAsleep).
func readHwmon(dir string) []Reading {
	files, _ := filepath.Glob(filepath.Join(dir, "hwmon*", "power*_average"))
	inputs, _ := filepath.Glob(filepath.Join(dir, "hwmon*", "power*_input"))
	for _, input := range inputs {
		average := strings.TrimSuffix(input, "_input") + "_average"
		if !slices.Contains(files, average) {
			files = append(files, input)
		}
	}
	slices.Sort(files)
	var readings []Reading
	for _, file := range files {
		sensor := filepath.Dir(file)
		microwatts := uint64(0)
		if !metrics.HwmonAsleep(sensor) {
			var ok bool
			if microwatts, ok = readUint(file); !ok {
				continue
			}
		}
		base := filepath.Base(file)
		channel := base[:strings.LastIndex(base, "_")]
		device := sysfile.Text(filepath.Join(sensor, "name"))
		label := device
		if extra := sysfile.Text(filepath.Join(sensor, channel+"_label")); extra != "" {
			label += " " + extra
		}
		if label == "" {
			label = filepath.Base(sensor)
		}
		readings = append(readings, Reading{
			ID:    idOf("hwmon", sensorID(sensor, device), channel),
			Label: label,
			Watts: float64(microwatts) / 1e6,
		})
	}
	return readings
}

// sensorID names a sensor folder by what stays the same across reboots: the
// driver's name and the device it belongs to, such as amdgpu-0000:03:00.0,
// not the folder's number, which the kernel hands out in the order the
// drivers load.
func sensorID(sensor, name string) string {
	device := ""
	if target, err := os.Readlink(filepath.Join(sensor, "device")); err == nil {
		device = filepath.Base(target)
	}
	switch {
	case name != "" && device != "":
		return name + "-" + device
	case name != "":
		return name
	case device != "":
		return device
	default:
		return filepath.Base(sensor)
	}
}
