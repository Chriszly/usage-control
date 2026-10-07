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
// does, with the same names, but does not read those of a device that
// sleeps, such as a laptop's second GPU, as that would wake it (see
// HwmonAsleep): they come with their name only, and asleep tells which they
// are, by their index. Without hwmon sensors, as on older Raspberry Pi
// kernels, gopsutil reads the thermal zones.
func readSensors(ctx context.Context) ([]sensors.TemperatureStat, []bool) {
	hwmon := filepath.Join(hostPath("HOST_SYS", "/sys"), "class", "hwmon")
	files, _ := filepath.Glob(filepath.Join(hwmon, "hwmon*", "temp*_input"))
	if len(files) == 0 {
		// Some kernels keep the sensors in the device folder.
		files, _ = filepath.Glob(filepath.Join(hwmon, "hwmon*", "device", "temp*_input"))
	}
	if len(files) == 0 {
		// gopsutil returns the sensors it could read together with an error
		// when others failed.
		readings, _ := sensors.TemperaturesWithContext(ctx)
		return readings, nil
	}
	sleepingDirs := map[string]bool{}
	readings := make([]sensors.TemperatureStat, 0, len(files))
	asleep := make([]bool, 0, len(files))
	for _, file := range files {
		dir := filepath.Dir(file)
		sleeps, checked := sleepingDirs[dir]
		if !checked {
			sleeps = HwmonAsleep(dir)
			sleepingDirs[dir] = sleeps
		}
		// The name and label are what the driver keeps, so reading them
		// does not wake the device.
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
		reading := sensors.TemperatureStat{SensorKey: name}
		if !sleeps {
			milli, err := strconv.ParseFloat(sysfile.Text(file), 64)
			if err != nil {
				continue
			}
			reading.Temperature = milli / 1000
		}
		readings = append(readings, reading)
		asleep = append(asleep, sleeps)
	}
	return readings, asleep
}
