package metrics

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/Chriszly/usage-control/backend/internal/sysfile"
)

// Fan is the speed of one fan, such as the Raspberry Pi 5's cooling fan.
type Fan struct {
	Name string  `json:"name"`
	RPM  float64 `json:"rpm"`
}

// fanSensor is a file Linux reports a fan's speed in, and the fan's name.
type fanSensor struct {
	name string
	file string
}

// fanSensors returns the fans Linux reports in /sys/class/hwmon, looked up
// once at start. There are none on other systems, and on many PCs whose fans
// only the firmware knows about.
func fanSensors() []fanSensor {
	files, _ := filepath.Glob(filepath.Join(hostPath("HOST_SYS", "/sys"), "class", "hwmon", "hwmon*", "fan*_input"))
	sensors := make([]fanSensor, 0, len(files))
	for _, file := range files {
		fan := strings.TrimSuffix(filepath.Base(file), "_input")
		name := sysfile.Text(filepath.Join(filepath.Dir(file), fan+"_label"))
		if name == "" {
			name = strings.TrimSpace(sysfile.Text(filepath.Join(filepath.Dir(file), "name")) + " " + fan)
		}
		sensors = append(sensors, fanSensor{name: name, file: file})
	}
	slices.SortFunc(sensors, func(a, b fanSensor) int { return strings.Compare(a.name, b.name) })
	return sensors
}

// readFans returns the speed of each fan that can be read.
func readFans(sensors []fanSensor) []Fan {
	fans := make([]Fan, 0, len(sensors))
	for _, s := range sensors {
		rpm, ok := sysfile.Uint(s.file)
		if !ok {
			continue
		}
		fans = append(fans, Fan{Name: s.name, RPM: float64(rpm)})
	}
	return fans
}
