package metrics

import (
	"path/filepath"
	"strings"

	"github.com/Chriszly/usage-control/backend/internal/sysfile"
)

// This part tells whether Linux has put a GPU to sleep through runtime power
// management, as on laptops with a second GPU that only wakes for games.
// Asking such a GPU for its usage, as nvidia-smi does, or for its
// temperature, fan or power in /sys/class/hwmon, wakes it and keeps it awake.
// It has no build constraint, as the add-ons call it on every OS; elsewhere
// the /sys files are missing, so no GPU is found asleep.

// isSuspended reports whether the kernel has put a PCI device to sleep, from
// its power/runtime_status: one small file that does not wake it.
func isSuspended(device string) bool {
	return sysfile.Text(filepath.Join(device, "power", "runtime_status")) == "suspended"
}

// HwmonAsleep reports whether the device of a sensor folder in
// /sys/class/hwmon, such as an AMD GPU's, sleeps, so its sensors are not
// read: reading them would wake it. dir is the hwmon folder, or its device
// folder where the sensors are kept there.
func HwmonAsleep(dir string) bool {
	return isSuspended(filepath.Join(dir, "device")) || isSuspended(dir)
}

// NvidiaSleep tells whether the NVIDIA GPUs sleep, so nvidia-smi is not
// started then. A nil NvidiaSleep never finds them asleep.
type NvidiaSleep struct {
	// devices are the NVIDIA GPUs' folders in /sys/bus/pci/devices.
	devices []string
}

// NewNvidiaSleep looks up the NVIDIA GPUs once, in sysDir/bus/pci/devices: a
// GPU is part of the machine, so the list does not change while it runs.
// sysDir is where /sys is, which in a container is where the host's /sys is
// mounted.
func NewNvidiaSleep(sysDir string) *NvidiaSleep {
	devices, _ := filepath.Glob(filepath.Join(sysDir, "bus", "pci", "devices", "*"))
	s := &NvidiaSleep{}
	for _, device := range devices {
		// 0x10de is NVIDIA, and classes 0x03xxxx are display controllers,
		// leaving out the sound and USB parts of a graphics card.
		if sysfile.Text(filepath.Join(device, "vendor")) == "0x10de" &&
			strings.HasPrefix(sysfile.Text(filepath.Join(device, "class")), "0x03") {
			s.devices = append(s.devices, device)
		}
	}
	return s
}

// Asleep reports whether every NVIDIA GPU sleeps; false when none was found.
func (s *NvidiaSleep) Asleep() bool {
	if s == nil || len(s.devices) == 0 {
		return false
	}
	for _, device := range s.devices {
		if !isSuspended(device) {
			return false
		}
	}
	return true
}
