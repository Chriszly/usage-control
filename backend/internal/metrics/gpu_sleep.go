package metrics

import (
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/sysfile"
)

// This part tells whether Linux has put a GPU to sleep through runtime power
// management, as on laptops with a second GPU that only wakes for games.
// Asking such a GPU for its usage, as nvidia-smi does, or for its
// temperature, fan or power in /sys/class/hwmon, wakes it and keeps it awake.
// An awake GPU that may sleep, whose power/control is "auto", stays awake
// for its power/autosuspend_delay_ms after each such read, so reading it
// every few seconds would keep it from ever falling asleep: its sensors are
// read, and nvidia-smi started, at most every wakeDelays times that delay,
// and the last values are shown in between.
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

// HwmonRead reads file, a sensor in the hwmon folder dir as HwmonAsleep
// takes it, or returns its last content instead while the device may sleep
// and was read less than wakeDelays autosuspend delays ago (see
// deviceReads).
func HwmonRead(dir, file string) ([]byte, error) {
	return sensorReads.read(file, filepath.Join(dir, "device"), dir)
}

// hwmonUint reads a sensor file that holds one whole number through
// HwmonRead; false when it cannot be read or holds no such number.
func hwmonUint(dir, file string) (uint64, bool) {
	text, err := HwmonRead(dir, file)
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(text)), 10, 64)
	return n, err == nil
}

const (
	// wakeDelays is how many of its autosuspend delays pass between two
	// reads of a device that may sleep, so it has the time to fall asleep.
	wakeDelays = 2
	// decisionTime is how long the decision whether to read a device again
	// holds, so the values read at one moment, such as an AMD GPU's usage,
	// temperature and fan, all come new or all come from the last read.
	decisionTime = time.Second
)

// deviceReads reads the sensor files of devices that may sleep at most every
// wakeDelays autosuspend delays, and keeps the last content of each file for
// the reads in between. A device that may not sleep, whose power/control is
// "on", that has no autosuspend delay, or that is the GPU the display
// started on, is read every time.
type deviceReads struct {
	now func() time.Time

	// mu guards the decisions by the device folder asked about, when each
	// device that may sleep was last read, by its real folder as /sys
	// reaches one device through several links, and the last content of
	// each file.
	mu        sync.Mutex
	decisions map[string]readDecision
	reads     map[string]time.Time
	values    map[string]fileContent
}

type readDecision struct {
	at   time.Time
	read bool
}

type fileContent struct {
	data []byte
	err  error
}

// sensorReads is the program's one deviceReads, so every read of a device
// counts towards when it is read again.
var sensorReads = newDeviceReads(time.Now)

func newDeviceReads(now func() time.Time) *deviceReads {
	return &deviceReads{
		now:       now,
		decisions: map[string]readDecision{},
		reads:     map[string]time.Time{},
		values:    map[string]fileContent{},
	}
}

// due reports whether device may be read now. Its power/control and
// power/autosuspend_delay_ms, which the kernel keeps without waking it, are
// read at most once per decisionTime.
func (d *deviceReads) due(device string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	if last, ok := d.decisions[device]; ok && now.Sub(last.at) >= 0 && now.Sub(last.at) < decisionTime {
		return last.read
	}
	read := true
	// The GPU the machine started its display on, whose boot_vga the kernel
	// keeps at 1 without waking it, drives the monitor and does not sleep,
	// although desktop AMD GPUs allow it (power/control is "auto").
	bootDisplay := sysfile.Text(filepath.Join(device, "boot_vga")) == "1"
	if !bootDisplay && sysfile.Text(filepath.Join(device, "power", "control")) == "auto" {
		// The delay is negative when the device does not autosuspend, and
		// cannot be read when its driver does not use it.
		ms, err := strconv.Atoi(sysfile.Text(filepath.Join(device, "power", "autosuspend_delay_ms")))
		if err == nil && ms > 0 {
			key := device
			if resolved, err := filepath.EvalSymlinks(device); err == nil {
				key = resolved
			}
			last, seen := d.reads[key]
			since := now.Sub(last)
			switch {
			case seen && since >= 0 && since < decisionTime:
				// The same moment, read through another link to the device.
			case seen && since >= 0 && since < wakeDelays*time.Duration(ms)*time.Millisecond:
				read = false
			default:
				d.reads[key] = now
			}
		}
	}
	d.decisions[device] = readDecision{at: now, read: read}
	return read
}

// read returns the content of file, a sensor of devices, or its last content
// while one of them is not due to be read.
func (d *deviceReads) read(file string, devices ...string) ([]byte, error) {
	due := true
	for _, device := range devices {
		due = d.due(device) && due
	}
	d.mu.Lock()
	last, ok := d.values[file]
	d.mu.Unlock()
	if !due && ok {
		return last.data, last.err
	}
	data, err := sysfile.Read(file)
	d.mu.Lock()
	d.values[file] = fileContent{data: data, err: err}
	d.mu.Unlock()
	return data, err
}

// nvidiaListInterval is how often NvidiaSleep looks for the NVIDIA GPUs
// again, so one plugged in later, such as an external GPU, is found.
const nvidiaListInterval = time.Minute

// NvidiaSleep tells whether the NVIDIA GPUs sleep, so nvidia-smi is not
// started then, and whether one that may sleep was asked too recently. A nil
// NvidiaSleep never finds them asleep.
type NvidiaSleep struct {
	// sysDir is where /sys is, which in a container is where the host's
	// /sys is mounted.
	sysDir string
	// reads decides when nvidia-smi may be started again: sensorReads,
	// shared with the program's other reads.
	reads *deviceReads

	// mu guards the NVIDIA GPUs' folders in /sys/bus/pci/devices and when
	// they were looked up.
	mu      sync.Mutex
	devices []string
	listed  time.Time
}

// NewNvidiaSleep returns an NvidiaSleep for the NVIDIA GPUs in
// sysDir/bus/pci/devices, which it looks up every nvidiaListInterval.
func NewNvidiaSleep(sysDir string) *NvidiaSleep {
	return &NvidiaSleep{sysDir: sysDir, reads: sensorReads}
}

// NewNvidiaSleepWithClock returns an NvidiaSleep like NewNvidiaSleep that
// tells when nvidia-smi is due by now, not by the time of day, and on its
// own, not counting the program's other reads of the GPUs; for tests.
func NewNvidiaSleepWithClock(sysDir string, now func() time.Time) *NvidiaSleep {
	return &NvidiaSleep{sysDir: sysDir, reads: newDeviceReads(now)}
}

// gpus returns the NVIDIA GPUs' folders, looked up again once the list is
// nvidiaListInterval old.
func (s *NvidiaSleep) gpus() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.listed.IsZero() && time.Since(s.listed) < nvidiaListInterval {
		return s.devices
	}
	devices, _ := filepath.Glob(filepath.Join(s.sysDir, "bus", "pci", "devices", "*"))
	s.devices, s.listed = nil, time.Now()
	for _, device := range devices {
		// 0x10de is NVIDIA, and classes 0x03xxxx are display controllers,
		// leaving out the sound and USB parts of a graphics card.
		if sysfile.Text(filepath.Join(device, "vendor")) == "0x10de" &&
			strings.HasPrefix(sysfile.Text(filepath.Join(device, "class")), "0x03") {
			s.devices = append(s.devices, device)
		}
	}
	return s.devices
}

// Asleep reports whether every NVIDIA GPU sleeps; false when none was found.
func (s *NvidiaSleep) Asleep() bool {
	if s == nil {
		return false
	}
	devices := s.gpus()
	if len(devices) == 0 {
		return false
	}
	for _, device := range devices {
		if !isSuspended(device) {
			return false
		}
	}
	return true
}

// Due reports whether nvidia-smi may be started now: not while an NVIDIA GPU
// that may sleep was asked less than wakeDelays autosuspend delays ago (see
// deviceReads). A true answer counts as asking, so it is called right before
// nvidia-smi is started. A nil NvidiaSleep is always due.
func (s *NvidiaSleep) Due() bool {
	if s == nil {
		return true
	}
	due := true
	for _, device := range s.gpus() {
		due = s.reads.due(device) && due
	}
	return due
}
