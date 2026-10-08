package metrics

import (
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
