package power

import (
	"path/filepath"
	"slices"
	"strings"
)

// readHwmon reads the power sensors the kernel lists under
// /sys/class/hwmon, such as an AMD GPU's: power*_average, else
// power*_input, in microwatts.
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
		microwatts, ok := readUint(file)
		if !ok {
			continue
		}
		sensor := filepath.Dir(file)
		base := filepath.Base(file)
		channel := base[:strings.LastIndex(base, "_")]
		device := readText(filepath.Join(sensor, "name"))
		label := device
		if extra := readText(filepath.Join(sensor, channel+"_label")); extra != "" {
			label += " " + extra
		}
		if label == "" {
			label = filepath.Base(sensor)
		}
		readings = append(readings, Reading{
			ID:    idOf("hwmon", filepath.Base(sensor), channel),
			Label: label,
			Watts: float64(microwatts) / 1e6,
		})
	}
	return readings
}
