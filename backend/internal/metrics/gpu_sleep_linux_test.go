package metrics

import (
	"os"
	"path/filepath"
	"testing"
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
