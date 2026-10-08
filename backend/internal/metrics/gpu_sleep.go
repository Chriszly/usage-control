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
	// forgetTime is how long a device or file not asked about is kept, so
	// those gone, as when hwmon or PCI numbering changed, are dropped.
	forgetTime = 10 * time.Minute
)

// deviceReads reads the sensor files of devices that may sleep at most every
// wakeDelays autosuspend delays, and keeps the last content of each file for
// the reads in between. A device that may not sleep, whose power/control is
// "on", that has no autosuspend delay, or that drives a display, is read
// every time.
type deviceReads struct {
	now func() time.Time

	// mu guards the devices by their real folder, as /sys reaches one device
	// through several links, the real folder of each folder asked about, the
	// last content of each file, and when those not asked about were last
	// dropped.
	mu      sync.Mutex
	devices map[string]deviceState
	links   map[string]deviceLink
	values  map[string]fileContent
	pruned  time.Time
	// amdgpuRunpm returns the amdgpu driver's runpm setting, read once, as
	// it is fixed while the driver is loaded.
	amdgpuRunpm func() string
}

// amdgpuRunpmFile holds the amdgpu driver's runpm setting.
const amdgpuRunpmFile = "/sys/module/amdgpu/parameters/runpm"

// deviceLink is the real folder of a folder asked about, looked up at
// resolved. It is looked up again once the decision made then has expired,
// so the few files read at one moment resolve it once, and a folder that
// leads to another device after the numbering changed is followed.
type deviceLink struct {
	real     string
	resolved time.Time
}

// deviceState is the decision whether to read a device, made at decided and
// held for decisionTime, and when it was last read while it may sleep.
type deviceState struct {
	decided  time.Time
	read     bool
	lastRead time.Time
}

type fileContent struct {
	data  []byte
	err   error
	asked time.Time
}

// sensorReads is the program's one deviceReads, so every read of a device
// counts towards when it is read again.
var sensorReads = newDeviceReads(time.Now)

func newDeviceReads(now func() time.Time) *deviceReads {
	return &deviceReads{
		now:     now,
		devices: map[string]deviceState{},
		links:   map[string]deviceLink{},
		values:  map[string]fileContent{},
		amdgpuRunpm: sync.OnceValue(func() string {
			return sysfile.Text(amdgpuRunpmFile)
		}),
	}
}

// due reports whether device may be read now. Its power/control and
// power/autosuspend_delay_ms, which the kernel keeps without waking it, are
// read at most once per decisionTime, and that one decision holds for every
// link to the device.
func (d *deviceReads) due(device string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	d.prune(now)
	link, ok := d.links[device]
	if since := now.Sub(link.resolved); !ok || since < 0 || since >= decisionTime {
		link = deviceLink{real: device, resolved: now}
		if resolved, err := filepath.EvalSymlinks(device); err == nil {
			link.real = resolved
		}
		d.links[device] = link
	}
	device = link.real
	state := d.devices[device]
	if since := now.Sub(state.decided); !state.decided.IsZero() && since >= 0 && since < decisionTime {
		return state.read
	}
	state.decided, state.read = now, true
	if sysfile.Text(filepath.Join(device, "power", "control")) == "auto" && !d.drivesDisplay(device) {
		// The delay is negative when the device does not autosuspend, and
		// cannot be read when its driver does not use it.
		ms, err := strconv.Atoi(sysfile.Text(filepath.Join(device, "power", "autosuspend_delay_ms")))
		if err == nil && ms > 0 {
			since := now.Sub(state.lastRead)
			if !state.lastRead.IsZero() && since >= 0 && since < wakeDelays*time.Duration(ms)*time.Millisecond {
				state.read = false
			} else {
				state.lastRead = now
			}
		}
	}
	d.devices[device] = state
	return state.read
}

// prune drops, at most once per forgetTime, the devices, folders and files
// not asked about for forgetTime. d.mu is held.
func (d *deviceReads) prune(now time.Time) {
	if since := now.Sub(d.pruned); since >= 0 && since < forgetTime {
		return
	}
	d.pruned = now
	for device, state := range d.devices {
		if now.Sub(state.decided) >= forgetTime {
			delete(d.devices, device)
		}
	}
	for device, link := range d.links {
		if now.Sub(link.resolved) >= forgetTime {
			delete(d.links, device)
		}
	}
	for file, value := range d.values {
		if now.Sub(value.asked) >= forgetTime {
			delete(d.values, file)
		}
	}
}

// drivesDisplay reports whether a GPU drives a display, so it does not sleep,
// although desktop AMD GPUs allow it (power/control is "auto"): the GPU the
// machine started its display on, whose boot_vga is 1, or one with a
// connector in use, such as a second card driving a second monitor. The
// kernel keeps the files read here without waking the GPU. A connector is in
// use when its drm/card*/card*-*/enabled is "enabled" and its dpms is "On",
// not when its monitor is switched off in the display settings or only
// blanked (DPMS off), which leaves enabled set but turns dpms to "Off" in
// drivers that keep it up to date, as amdgpu does. amdgpu, though, keeps its
// GPU awake while any monitor is connected (since Linux 6.0), blanked or
// not, unless its runpm is -2, so there a connector whose status is
// "connected" is in use too. A connector without dpms counts by enabled
// alone.
func (d *deviceReads) drivesDisplay(device string) bool {
	if sysfile.Text(filepath.Join(device, "boot_vga")) == "1" {
		return true
	}
	connectors, _ := filepath.Glob(filepath.Join(device, "drm", "card*", "card*-*"))
	if len(connectors) == 0 {
		return false
	}
	awakeWhileConnected := false
	if driver, err := filepath.EvalSymlinks(filepath.Join(device, "driver")); err == nil && filepath.Base(driver) == "amdgpu" {
		awakeWhileConnected = d.amdgpuRunpm() != "-2"
	}
	for _, connector := range connectors {
		if awakeWhileConnected && sysfile.Text(filepath.Join(connector, "status")) == "connected" {
			return true
		}
		if sysfile.Text(filepath.Join(connector, "enabled")) != "enabled" {
			continue
		}
		switch sysfile.Text(filepath.Join(connector, "dpms")) {
		case "Off", "Standby", "Suspend":
		default:
			return true
		}
	}
	return false
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
	if ok && !due {
		last.asked = d.now()
		d.values[file] = last
	}
	d.mu.Unlock()
	if !due && ok {
		return last.data, last.err
	}
	data, err := sysfile.Read(file)
	d.mu.Lock()
	d.values[file] = fileContent{data: data, err: err, asked: d.now()}
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
