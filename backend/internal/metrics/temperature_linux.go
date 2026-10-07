package metrics

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Chriszly/usage-control/backend/internal/sysfile"
	"github.com/shirou/gopsutil/v4/sensors"
)

// readSensors reads the temperature sensors in /sys/class/hwmon as gopsutil
// does, with the same names, but leaves out those of a device that sleeps,
// such as a laptop's second GPU, as reading them would wake it (see
// HwmonAsleep). Without hwmon sensors, as on older Raspberry Pi kernels,
// gopsutil reads the thermal zones.
func readSensors(ctx context.Context) ([]sensors.TemperatureStat, error) {
	hwmon := filepath.Join(hostPath("HOST_SYS", "/sys"), "class", "hwmon")
	files, _ := filepath.Glob(filepath.Join(hwmon, "hwmon*", "temp*_input"))
	if len(files) == 0 {
		// Some kernels keep the sensors in the device folder.
		files, _ = filepath.Glob(filepath.Join(hwmon, "hwmon*", "device", "temp*_input"))
	}
	if len(files) == 0 {
		return sensors.TemperaturesWithContext(ctx)
	}
	asleep := map[string]bool{}
	readings := make([]sensors.TemperatureStat, 0, len(files))
	for _, file := range files {
		dir := filepath.Dir(file)
		sleeps, checked := asleep[dir]
		if !checked {
			sleeps = HwmonAsleep(dir)
			asleep[dir] = sleeps
		}
		if sleeps {
			continue
		}
		raw, err := sysfile.Read(filepath.Join(dir, "name"))
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(raw))
		// temp1_input has its label, such as "Core 0", in temp1_label,
		// which names the sensor as "coretemp_core_0".
		channel := strings.TrimSuffix(filepath.Base(file), "_input")
		if label := sysfile.Text(filepath.Join(dir, channel+"_label")); label != "" {
			name += "_" + strings.ReplaceAll(strings.ToLower(label), " ", "_")
		}
		milli, err := strconv.ParseFloat(sysfile.Text(file), 64)
		if err != nil {
			continue
		}
		readings = append(readings, sensors.TemperatureStat{SensorKey: name, Temperature: milli / 1000})
	}
	return readings, nil
}
