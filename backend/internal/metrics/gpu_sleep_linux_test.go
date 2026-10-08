package metrics

import (
	"cmp"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeSysFile writes a file below dir, making its folders.
func writeSysFile(t *testing.T, dir, path, text string) {
	t.Helper()
	path = filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestNvidiaSleepFindsTheNvidiaGPUsAsleep(t *testing.T) {
	sys := t.TempDir()
	gpu := "bus/pci/devices/0000:01:00.0/"
	writeSysFile(t, sys, gpu+"vendor", "0x10de\n")
	writeSysFile(t, sys, gpu+"class", "0x030200\n")
	writeSysFile(t, sys, gpu+"power/runtime_status", "suspended\n")
	// The card's sound part, which sleeps on its own.
	writeSysFile(t, sys, "bus/pci/devices/0000:01:00.1/vendor", "0x10de\n")
	writeSysFile(t, sys, "bus/pci/devices/0000:01:00.1/class", "0x040300\n")
	writeSysFile(t, sys, "bus/pci/devices/0000:01:00.1/power/runtime_status", "active\n")
	// Another maker's GPU, awake.
	writeSysFile(t, sys, "bus/pci/devices/0000:00:02.0/vendor", "0x8086\n")
	writeSysFile(t, sys, "bus/pci/devices/0000:00:02.0/class", "0x030000\n")
	writeSysFile(t, sys, "bus/pci/devices/0000:00:02.0/power/runtime_status", "active\n")

	sleep := NewNvidiaSleep(sys)
	if !sleep.Asleep() {
		t.Error("Asleep() = false for a suspended NVIDIA GPU, want true")
	}
	writeSysFile(t, sys, gpu+"power/runtime_status", "active\n")
	if sleep.Asleep() {
		t.Error("Asleep() = true for an active NVIDIA GPU, want false")
	}
	var none *NvidiaSleep
	if none.Asleep() || NewNvidiaSleep(t.TempDir()).Asleep() {
		t.Error("Asleep() = true without NVIDIA GPUs, want false")
	}
}

func TestDeviceReadsLetADeviceThatMaySleepFallAsleep(t *testing.T) {
	sys := t.TempDir()
	device := filepath.Join(sys, "devices", "0000:03:00.0")
	writeSysFile(t, sys, "devices/0000:03:00.0/power/control", "auto\n")
	writeSysFile(t, sys, "devices/0000:03:00.0/power/autosuspend_delay_ms", "5000\n")
	writeSysFile(t, sys, "devices/0000:03:00.0/gpu_busy_percent", "10\n")
	// The same device through another link, as /sys/class/drm/card1/device.
	link := filepath.Join(sys, "card1-device")
	if err := os.Symlink(device, link); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	reads := newDeviceReads(func() time.Time { return now })
	read := func(device string) string {
		t.Helper()
		data, err := reads.read(filepath.Join(device, "gpu_busy_percent"), device)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(data))
	}

	if got := read(device); got != "10" {
		t.Fatalf("first read = %s, want 10", got)
	}
	writeSysFile(t, sys, "devices/0000:03:00.0/gpu_busy_percent", "20\n")
	// At the same moment, through the other link, it is read too.
	now = now.Add(decisionTime / 2)
	if got := read(link); got != "20" {
		t.Errorf("read through another link at the same moment = %s, want 20", got)
	}
	writeSysFile(t, sys, "devices/0000:03:00.0/gpu_busy_percent", "30\n")
	now = now.Add(2 * time.Second)
	if got := read(link); got != "20" {
		t.Errorf("read within twice the autosuspend delay = %s, want the last value, 20", got)
	}
	now = now.Add(10 * time.Second)
	if got := read(link); got != "30" {
		t.Errorf("read after twice the autosuspend delay = %s, want 30", got)
	}

	// A device that may not sleep is read every time.
	writeSysFile(t, sys, "devices/0000:03:00.0/power/control", "on\n")
	writeSysFile(t, sys, "devices/0000:03:00.0/gpu_busy_percent", "40\n")
	now = now.Add(2 * time.Second)
	if got := read(device); got != "40" {
		t.Errorf("read of a device that may not sleep = %s, want 40", got)
	}
}

func TestDeviceReadsReadTheBootDisplayGPUEveryTime(t *testing.T) {
	sys := t.TempDir()
	// A desktop's AMD GPU driving the monitor: it allows runtime power
	// management, but does not sleep while it drives the display.
	device := filepath.Join(sys, "devices", "0000:03:00.0")
	writeSysFile(t, sys, "devices/0000:03:00.0/boot_vga", "1\n")
	writeSysFile(t, sys, "devices/0000:03:00.0/power/control", "auto\n")
	writeSysFile(t, sys, "devices/0000:03:00.0/power/autosuspend_delay_ms", "5000\n")
	writeSysFile(t, sys, "devices/0000:03:00.0/gpu_busy_percent", "10\n")
	now := time.Now()
	reads := newDeviceReads(func() time.Time { return now })
	file := filepath.Join(device, "gpu_busy_percent")

	if _, err := reads.read(file, device); err != nil {
		t.Fatal(err)
	}
	writeSysFile(t, sys, "devices/0000:03:00.0/gpu_busy_percent", "20\n")
	now = now.Add(2 * time.Second)
	data, err := reads.read(file, device)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); got != "20" {
		t.Errorf("read of the boot display GPU 2 seconds later = %s, want 20", got)
	}
}

func TestNvidiaSleepFindsAGPUPluggedInLater(t *testing.T) {
	sys := t.TempDir()
	sleep := NewNvidiaSleep(sys)
	if sleep.Asleep() {
		t.Fatal("Asleep() = true without NVIDIA GPUs, want false")
	}
	gpu := "bus/pci/devices/0000:05:00.0/"
	writeSysFile(t, sys, gpu+"vendor", "0x10de\n")
	writeSysFile(t, sys, gpu+"class", "0x030000\n")
	writeSysFile(t, sys, gpu+"power/runtime_status", "suspended\n")
	if sleep.Asleep() {
		t.Error("Asleep() = true before the GPUs are looked up again, want false")
	}
	sleep.listed = sleep.listed.Add(-nvidiaListInterval)
	if !sleep.Asleep() {
		t.Error("Asleep() = false for a suspended NVIDIA GPU plugged in later, want true")
	}
}

func TestNvidiaSleepDueWaitsForAGPUThatMaySleep(t *testing.T) {
	sys := t.TempDir()
	gpu := "bus/pci/devices/0000:01:00.0/"
	writeSysFile(t, sys, gpu+"vendor", "0x10de\n")
	writeSysFile(t, sys, gpu+"class", "0x030000\n")
	writeSysFile(t, sys, gpu+"power/runtime_status", "active\n")
	writeSysFile(t, sys, gpu+"power/control", "auto\n")
	writeSysFile(t, sys, gpu+"power/autosuspend_delay_ms", "5000\n")
	now := time.Now()
	sleep := NewNvidiaSleep(sys)
	sleep.reads = newDeviceReads(func() time.Time { return now })

	if !sleep.Due() {
		t.Error("Due() at first = false, want true")
	}
	now = now.Add(5 * time.Second)
	if sleep.Due() {
		t.Error("Due() within twice the autosuspend delay = true, want false")
	}
	now = now.Add(5 * time.Second)
	if !sleep.Due() {
		t.Error("Due() after twice the autosuspend delay = false, want true")
	}
	var none *NvidiaSleep
	if !none.Due() {
		t.Error("Due() of a nil NvidiaSleep = false, want true")
	}
}

func TestDeviceReadsReadAGPUDrivingAMonitorEveryTime(t *testing.T) {
	sys := t.TempDir()
	// A desktop's second AMD GPU, driving a second monitor, though the
	// display started on the first.
	device := filepath.Join(sys, "devices", "0000:03:00.0")
	writeSysFile(t, sys, "devices/0000:03:00.0/boot_vga", "0\n")
	writeSysFile(t, sys, "devices/0000:03:00.0/power/control", "auto\n")
	writeSysFile(t, sys, "devices/0000:03:00.0/power/autosuspend_delay_ms", "5000\n")
	writeSysFile(t, sys, "devices/0000:03:00.0/drm/card1/card1-HDMI-A-1/enabled", "disabled\n")
	writeSysFile(t, sys, "devices/0000:03:00.0/drm/card1/card1-DP-1/enabled", "enabled\n")
	writeSysFile(t, sys, "devices/0000:03:00.0/gpu_busy_percent", "10\n")
	now := time.Now()
	reads := newDeviceReads(func() time.Time { return now })
	file := filepath.Join(device, "gpu_busy_percent")
	read := func() string {
		t.Helper()
		data, err := reads.read(file, device)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(data))
	}

	read()
	writeSysFile(t, sys, "devices/0000:03:00.0/gpu_busy_percent", "20\n")
	now = now.Add(2 * time.Second)
	if got := read(); got != "20" {
		t.Errorf("read of a GPU driving a monitor 2 seconds later = %s, want 20", got)
	}

	// While the monitor is only blanked, the connector stays enabled but
	// its dpms is off, so the GPU may sleep.
	writeSysFile(t, sys, "devices/0000:03:00.0/drm/card1/card1-DP-1/dpms", "Off\n")
	writeSysFile(t, sys, "devices/0000:03:00.0/gpu_busy_percent", "21\n")
	now = now.Add(2 * time.Second)
	if got := read(); got != "21" {
		t.Errorf("first read once the monitor is blanked = %s, want 21", got)
	}
	writeSysFile(t, sys, "devices/0000:03:00.0/gpu_busy_percent", "22\n")
	now = now.Add(2 * time.Second)
	if got := read(); got != "21" {
		t.Errorf("read within twice the autosuspend delay once the monitor is blanked = %s, want the last value, 21", got)
	}

	// Once it shows again, the GPU is read every time again.
	writeSysFile(t, sys, "devices/0000:03:00.0/drm/card1/card1-DP-1/dpms", "On\n")
	now = now.Add(10 * time.Second)
	read()
	writeSysFile(t, sys, "devices/0000:03:00.0/gpu_busy_percent", "23\n")
	now = now.Add(2 * time.Second)
	if got := read(); got != "23" {
		t.Errorf("read of a GPU driving an unblanked monitor 2 seconds later = %s, want 23", got)
	}

	// Once the monitor is no longer in use, it may sleep.
	writeSysFile(t, sys, "devices/0000:03:00.0/drm/card1/card1-DP-1/enabled", "disabled\n")
	writeSysFile(t, sys, "devices/0000:03:00.0/gpu_busy_percent", "30\n")
	now = now.Add(2 * time.Second)
	if got := read(); got != "30" {
		t.Errorf("first read once the monitor is off = %s, want 30", got)
	}
	writeSysFile(t, sys, "devices/0000:03:00.0/gpu_busy_percent", "40\n")
	now = now.Add(2 * time.Second)
	if got := read(); got != "30" {
		t.Errorf("read within twice the autosuspend delay once the monitor is off = %s, want the last value, 30", got)
	}
}

func TestDeviceReadsLetAGPUWithABlankedMonitorSleep(t *testing.T) {
	for _, test := range []struct {
		name string
		// driver is the GPU's driver, runpm amdgpu's runpm setting.
		driver, runpm, dpms string
		// status and enabled are the connector's, "connected" and
		// "enabled" when empty.
		status, enabled string
		// sleeps is whether the GPU may sleep, so it is not read every time.
		sleeps bool
	}{
		{name: "amdgpu, default runpm, blanked", driver: "amdgpu", runpm: "-1", dpms: "Off", sleeps: false},
		{name: "amdgpu, default runpm, showing", driver: "amdgpu", runpm: "-1", dpms: "On", sleeps: false},
		{name: "amdgpu, default runpm, disconnected", driver: "amdgpu", runpm: "-1", dpms: "Off", status: "disconnected", enabled: "disabled", sleeps: true},
		{name: "amdgpu, runpm -2, off", driver: "amdgpu", runpm: "-2", dpms: "Off", sleeps: true},
		{name: "amdgpu, runpm -2, standby", driver: "amdgpu", runpm: "-2", dpms: "Standby", sleeps: true},
		{name: "amdgpu, runpm -2, suspend", driver: "amdgpu", runpm: "-2", dpms: "Suspend", sleeps: true},
		{name: "amdgpu, runpm -2, showing", driver: "amdgpu", runpm: "-2", dpms: "On", sleeps: false},
		{name: "other driver, off", driver: "radeon", runpm: "-1", dpms: "Off", sleeps: true},
		{name: "other driver, standby", driver: "radeon", runpm: "-1", dpms: "Standby", sleeps: true},
		{name: "other driver, suspend", driver: "radeon", runpm: "-1", dpms: "Suspend", sleeps: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			sys := t.TempDir()
			// A desktop's second AMD GPU with a monitor connected and in
			// use, though the display started on the first.
			device := filepath.Join(sys, "devices", "0000:03:00.0")
			writeSysFile(t, sys, "devices/0000:03:00.0/boot_vga", "0\n")
			writeSysFile(t, sys, "devices/0000:03:00.0/power/control", "auto\n")
			writeSysFile(t, sys, "devices/0000:03:00.0/power/autosuspend_delay_ms", "5000\n")
			status, enabled := cmp.Or(test.status, "connected"), cmp.Or(test.enabled, "enabled")
			writeSysFile(t, sys, "devices/0000:03:00.0/drm/card1/card1-DP-1/status", status+"\n")
			writeSysFile(t, sys, "devices/0000:03:00.0/drm/card1/card1-DP-1/enabled", enabled+"\n")
			writeSysFile(t, sys, "devices/0000:03:00.0/drm/card1/card1-DP-1/dpms", test.dpms+"\n")
			writeSysFile(t, sys, "devices/0000:03:00.0/gpu_busy_percent", "10\n")
			driver := filepath.Join(sys, "bus", "pci", "drivers", test.driver)
			if err := os.MkdirAll(driver, 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(driver, filepath.Join(device, "driver")); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			reads := newDeviceReads(func() time.Time { return now })
			reads.runpm = test.runpm
			file := filepath.Join(device, "gpu_busy_percent")
			read := func() string {
				t.Helper()
				data, err := reads.read(file, device)
				if err != nil {
					t.Fatal(err)
				}
				return strings.TrimSpace(string(data))
			}

			read()
			writeSysFile(t, sys, "devices/0000:03:00.0/gpu_busy_percent", "20\n")
			now = now.Add(2 * time.Second)
			want := "20"
			if test.sleeps {
				want = "10"
			}
			if got := read(); got != want {
				t.Errorf("read 2 seconds later = %s, want %s", got, want)
			}
		})
	}
}

func TestDeviceReadsForgetWhatIsNoLongerAsked(t *testing.T) {
	// The devices are kept by their real folder.
	sys, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"0000:03:00.0", "0000:04:00.0"} {
		writeSysFile(t, sys, "devices/"+name+"/power/control", "auto\n")
		writeSysFile(t, sys, "devices/"+name+"/power/autosuspend_delay_ms", "5000\n")
		writeSysFile(t, sys, "devices/"+name+"/gpu_busy_percent", "10\n")
	}
	gone := filepath.Join(sys, "devices", "0000:03:00.0")
	kept := filepath.Join(sys, "devices", "0000:04:00.0")
	now := time.Now()
	reads := newDeviceReads(func() time.Time { return now })
	for _, device := range []string{gone, kept} {
		if _, err := reads.read(filepath.Join(device, "gpu_busy_percent"), device); err != nil {
			t.Fatal(err)
		}
	}
	// kept is asked about every minute, gone not again.
	for range 2 * forgetTime / time.Minute {
		now = now.Add(time.Minute)
		if _, err := reads.read(filepath.Join(kept, "gpu_busy_percent"), kept); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := reads.devices[gone]; ok {
		t.Error("a device not asked about for longer than forgetTime is still kept")
	}
	if _, ok := reads.values[filepath.Join(gone, "gpu_busy_percent")]; ok {
		t.Error("a file not asked about for longer than forgetTime is still kept")
	}
	if _, ok := reads.links[gone]; ok {
		t.Error("a folder not asked about for longer than forgetTime is still kept")
	}
	if _, ok := reads.devices[kept]; !ok {
		t.Error("a device still asked about was dropped")
	}
	if _, ok := reads.values[filepath.Join(kept, "gpu_busy_percent")]; !ok {
		t.Error("a file still asked about was dropped")
	}
}

func TestSensorsReportedForgetSensorsThatAreGone(t *testing.T) {
	reported := &sensorsReported{reported: map[string]bool{}}
	reported.set("/sys/class/hwmon/hwmon1/temp1_input", false)
	reported.set("/sys/class/hwmon/hwmon2/temp1_input", false)
	reported.keep([]string{"/sys/class/hwmon/hwmon2/temp1_input", "/sys/class/hwmon/hwmon3/temp1_input"})
	if _, ok := reported.reported["/sys/class/hwmon/hwmon1/temp1_input"]; ok {
		t.Error("a sensor that is gone is still kept")
	}
	if reported.get("/sys/class/hwmon/hwmon2/temp1_input") {
		t.Error("a sensor that is still listed was dropped")
	}
}

func TestDeviceReadsFollowALinkToAnotherDevice(t *testing.T) {
	sys, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(sys, "devices", "0000:03:00.0")
	second := filepath.Join(sys, "devices", "0000:04:00.0")
	writeSysFile(t, sys, "devices/0000:03:00.0/power/control", "on\n")
	writeSysFile(t, sys, "devices/0000:04:00.0/power/control", "on\n")
	link := filepath.Join(sys, "hwmon3-device")
	if err := os.Symlink(first, link); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	reads := newDeviceReads(func() time.Time { return now })
	reads.due(link)
	// The numbering changed: the same folder now leads to another device.
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(second, link); err != nil {
		t.Fatal(err)
	}
	// At the same moment, it is not looked up again.
	now = now.Add(decisionTime / 2)
	reads.due(link)
	if got := reads.links[link].real; got != first {
		t.Errorf("real folder at the same moment = %s, want %s", got, first)
	}
	now = now.Add(decisionTime)
	reads.due(link)
	if got := reads.links[link].real; got != second {
		t.Errorf("real folder once the decision expired = %s, want %s", got, second)
	}
}
