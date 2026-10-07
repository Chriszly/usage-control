package metrics

import (
	"reflect"
	"testing"
)

func TestReadSensorsLeavesSleepingDevicesAlone(t *testing.T) {
	sys := t.TempDir()
	writeSysFile(t, sys, "class/hwmon/hwmon0/name", "coretemp\n")
	writeSysFile(t, sys, "class/hwmon/hwmon0/temp1_input", "45000\n")
	writeSysFile(t, sys, "class/hwmon/hwmon0/temp1_label", "Core 0\n")
	writeSysFile(t, sys, "class/hwmon/hwmon1/name", "nvme\n")
	writeSysFile(t, sys, "class/hwmon/hwmon1/temp1_input", "38500\n")
	// A laptop's AMD GPU, asleep: reading its sensors would wake it.
	writeSysFile(t, sys, "class/hwmon/hwmon2/name", "amdgpu\n")
	writeSysFile(t, sys, "class/hwmon/hwmon2/temp1_input", "50000\n")
	writeSysFile(t, sys, "class/hwmon/hwmon2/temp1_label", "edge\n")
	writeSysFile(t, sys, "class/hwmon/hwmon2/device/power/runtime_status", "suspended\n")
	t.Setenv("HOST_SYS", sys)

	asleep := readTemperatures(t.Context())
	writeSysFile(t, sys, "class/hwmon/hwmon2/device/power/runtime_status", "active\n")
	awake := readTemperatures(t.Context())

	want := []Temperature{{Sensor: "coretemp_core_0", Celsius: 45}, {Sensor: "nvme", Celsius: 38.5}}
	if !reflect.DeepEqual(asleep, want) {
		t.Errorf("readTemperatures() while the GPU sleeps = %+v, want %+v", asleep, want)
	}
	want = append(want, Temperature{Sensor: "amdgpu_edge", Celsius: 50})
	if !reflect.DeepEqual(awake, want) {
		t.Errorf("readTemperatures() = %+v, want %+v", awake, want)
	}
}
