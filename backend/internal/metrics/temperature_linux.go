package metrics

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/Chriszly/usage-control/backend/internal/sysfile"
	"github.com/shirou/gopsutil/v4/sensors"
)

// readSensors reads the temperature sensors in /sys/class/hwmon as gopsutil
// does, with the same names, but does not read those of a device that
// sleeps, such as a laptop's second GPU, as that would wake it (see
// HwmonAsleep): they come with their name only, and asleep tells which they
// are, by their index. Such a sensor is left out when it reported no
// temperature when last read awake, so it counts in the numbering of
// same-named sensors only when it did then. Sensors of a device that may
// sleep are read only every few autosuspend delays (see HwmonRead). Without
// hwmon sensors, as on older Raspberry Pi kernels, gopsutil reads the
// thermal zones.
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
		if sleeps {
			if !reportedAwake.get(file) {
				continue
			}
		} else {
			text, err := HwmonRead(dir, file)
			milli, parseErr := strconv.ParseFloat(strings.TrimSpace(string(text)), 64)
			// temperaturesOf leaves out a temperature of 0 or less.
			reportedAwake.set(file, err == nil && parseErr == nil && milli > 0)
			if err != nil || parseErr != nil {
				continue
			}
			reading.Temperature = milli / 1000
		}
		readings = append(readings, reading)
		asleep = append(asleep, sleeps)
	}
	return readings, asleep
}

// sensorsReported holds, by its file, whether each sensor reported a
// temperature when it was last read awake.
type sensorsReported struct {
	mu       sync.Mutex
	reported map[string]bool
}

// reportedAwake is what the sensors reported when last read awake.
var reportedAwake = &sensorsReported{reported: map[string]bool{}}

// get reports whether the sensor in file reported a temperature when it was
// last read awake, or true when it was never read awake: its device has
// slept since usage-control started.
func (s *sensorsReported) get(file string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	reported, ok := s.reported[file]
	return reported || !ok
}

func (s *sensorsReported) set(file string, reported bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reported[file] = reported
}
