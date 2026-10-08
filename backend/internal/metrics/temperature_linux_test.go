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

func TestReadSensorsKeepsTheNamesWhileAGPUSleeps(t *testing.T) {
	sys := t.TempDir()
	// An AMD APU's GPU and a second AMD GPU, which name their sensors alike.
	for dir, celsius := range map[string]string{"hwmon0": "45000\n", "hwmon1": "50000\n"} {
		writeSysFile(t, sys, "class/hwmon/"+dir+"/name", "amdgpu\n")
		writeSysFile(t, sys, "class/hwmon/"+dir+"/temp1_input", celsius)
		writeSysFile(t, sys, "class/hwmon/"+dir+"/temp1_label", "edge\n")
	}
	t.Setenv("HOST_SYS", sys)
	awake := readTemperatures(t.Context())
	writeSysFile(t, sys, "class/hwmon/hwmon1/device/power/runtime_status", "suspended\n")

	asleep := readTemperatures(t.Context())

	if want := []Temperature{{Sensor: "amdgpu_edge 1", Celsius: 45}, {Sensor: "amdgpu_edge 2", Celsius: 50}}; !reflect.DeepEqual(awake, want) {
		t.Errorf("readTemperatures() = %+v, want %+v", awake, want)
	}
	if want := []Temperature{{Sensor: "amdgpu_edge 1", Celsius: 45}}; !reflect.DeepEqual(asleep, want) {
		t.Errorf("readTemperatures() while the second GPU sleeps = %+v, want %+v", asleep, want)
	}
}

func TestReadSensorsNumbersASleepingSensorAsWhenItWasLastAwake(t *testing.T) {
	sys := t.TempDir()
	writeSysFile(t, sys, "class/hwmon/hwmon0/name", "amdgpu\n")
	writeSysFile(t, sys, "class/hwmon/hwmon0/temp1_input", "45000\n")
	writeSysFile(t, sys, "class/hwmon/hwmon0/temp1_label", "edge\n")
	// A second GPU whose sensor reports no temperature while awake.
	writeSysFile(t, sys, "class/hwmon/hwmon1/name", "amdgpu\n")
	writeSysFile(t, sys, "class/hwmon/hwmon1/temp1_input", "0\n")
	writeSysFile(t, sys, "class/hwmon/hwmon1/temp1_label", "edge\n")
	t.Setenv("HOST_SYS", sys)
	awake := readTemperatures(t.Context())
	writeSysFile(t, sys, "class/hwmon/hwmon1/device/power/runtime_status", "suspended\n")

	asleep := readTemperatures(t.Context())

	want := []Temperature{{Sensor: "amdgpu_edge", Celsius: 45}}
	if !reflect.DeepEqual(awake, want) || !reflect.DeepEqual(asleep, want) {
		t.Errorf("readTemperatures() = %+v, then while the second GPU sleeps %+v, want %+v both times", awake, asleep, want)
	}
}

func TestReadSensorsWithoutHwmonReadsTheThermalZones(t *testing.T) {
	sys := t.TempDir()
	writeSysFile(t, sys, "class/thermal/thermal_zone0/type", "cpu_thermal\n")
	writeSysFile(t, sys, "class/thermal/thermal_zone0/temp", "45000\n")
	t.Setenv("HOST_SYS", sys)

	got := readTemperatures(t.Context())

	if want := []Temperature{{Sensor: "cpu_thermal", Celsius: 45}}; !reflect.DeepEqual(got, want) {
		t.Errorf("readTemperatures() = %+v, want %+v", got, want)
	}
}

func TestReadSensorsInTheDeviceFolder(t *testing.T) {
	sys := t.TempDir()
	// Sensors some kernels keep in the device folder, of a device asleep.
	writeSysFile(t, sys, "class/hwmon/hwmon0/device/name", "amdgpu\n")
	writeSysFile(t, sys, "class/hwmon/hwmon0/device/temp1_input", "50000\n")
	writeSysFile(t, sys, "class/hwmon/hwmon0/device/power/runtime_status", "suspended\n")
	writeSysFile(t, sys, "class/hwmon/hwmon1/device/name", "k10temp\n")
	writeSysFile(t, sys, "class/hwmon/hwmon1/device/temp1_input", "45000\n")
	t.Setenv("HOST_SYS", sys)

	got := readTemperatures(t.Context())

	if want := []Temperature{{Sensor: "k10temp", Celsius: 45}}; !reflect.DeepEqual(got, want) {
		t.Errorf("readTemperatures() = %+v, want %+v", got, want)
	}
}
