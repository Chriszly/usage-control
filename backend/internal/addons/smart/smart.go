// Package smart reads the health of the machine's disks for the smart
// add-on, through smartctl from smartmontools: whether the disk's own SMART
// check passes, its temperature, power-on hours, reallocated sectors or
// media errors and, for NVMe, how much of its rated wear is used.
//
// SMART reads cost time and wake disks that sleep, so it reads every
// ReadInterval, skips disks that are in standby, and reports the last result
// in between. It only reads; nothing in here changes the machine.
package smart

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	// ReadInterval is how often each disk is read.
	ReadInterval = 10 * time.Minute
	// ScanInterval is how often the list of disks is read again, for disks
	// plugged in or removed.
	ScanInterval = time.Hour
	// timeout is how long one call of smartctl may take.
	timeout = 30 * time.Second
)

// Exit status bits of smartctl: the command line was wrong, or the device
// could not be opened or was in standby (with -n standby). In both cases it
// read nothing.
const (
	exitCommandLine = 1 << 0
	exitOpenOrSleep = 1 << 1
)

// Disk is what was read of one disk. A value that the disk does not report
// is nil.
type Disk struct {
	// Name is the device, such as "/dev/sda" or "/dev/bus/0 megaraid,1", and
	// Model what the disk calls itself.
	Name  string
	Model string
	// Passed is whether the disk's own overall SMART check passes.
	Passed             *bool
	Celsius            *float64
	PowerOnHours       *float64
	ReallocatedSectors *float64
	MediaErrors        *float64
	PercentageUsed     *float64
}

// device is a disk smartctl found, and the type it opens it with. Disks
// behind a RAID controller share a name and differ in their type, such as
// "megaraid,1".
type device struct {
	name, kind string
}

// key tells devices apart.
func (d device) key() string {
	return d.name + " " + d.kind
}

// runFunc runs smartctl with args and returns what it wrote to its output.
type runFunc func(ctx context.Context, args ...string) ([]byte, error)

// Reader reads the disks' health in the background and returns the last
// result.
type Reader struct {
	run runFunc

	mu        sync.Mutex
	reading   bool
	scannedAt time.Time
	readAt    time.Time
	devices   []device
	disks     map[string]Disk
}

// NewReader returns a Reader that runs the smartctl on the PATH, or nil when
// smartmontools is not installed, which it logs.
func NewReader() *Reader {
	program, err := exec.LookPath("smartctl")
	if err != nil {
		slog.Warn("smartctl is not installed, so the add-on reports nothing; install smartmontools to read the disks' health")
		return nil
	}
	return newReader(func(ctx context.Context, args ...string) ([]byte, error) {
		// program is the smartctl found on the PATH at start; the arguments are
		// fixed or devices smartctl itself listed, checked by validName
		// and validKind.
		return exec.CommandContext(ctx, program, args...).Output() //nolint:gosec // see above
	})
}

func newReader(run runFunc) *Reader {
	return &Reader{run: run, disks: map[string]Disk{}}
}

// Read returns the disks read last, in the order smartctl lists them, and
// when a read is due starts it in the background, so a slow disk never holds
// up the report. A nil Reader returns nothing.
func (r *Reader) Read(ctx context.Context, now time.Time) []Disk {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.reading && (r.readAt.IsZero() || now.Sub(r.readAt) >= ReadInterval) {
		r.reading = true
		go r.refresh(ctx, now)
	}
	return r.last()
}

// last returns the disks read last; r.mu is held.
func (r *Reader) last() []Disk {
	var disks []Disk
	for _, d := range r.devices {
		if disk, ok := r.disks[d.key()]; ok {
			disks = append(disks, disk)
		}
	}
	return disks
}

// refresh lists the disks when that is due and reads each one.
func (r *Reader) refresh(ctx context.Context, now time.Time) {
	r.mu.Lock()
	devices := r.devices
	scan := r.scannedAt.IsZero() || now.Sub(r.scannedAt) >= ScanInterval
	r.mu.Unlock()

	scanned := false
	if scan {
		found, err := r.scan(ctx)
		if err != nil {
			slog.Warn("list the disks with smartctl", "error", err)
		} else {
			devices, scanned = found, true
		}
	}
	read := map[string]Disk{}
	for _, d := range devices {
		if disk, ok := r.readDisk(ctx, d); ok {
			read[d.key()] = disk
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	// A failed scan is tried again at the next read.
	if scanned {
		r.scannedAt = now
	}
	r.devices = devices
	// A disk that was asleep or could not be read keeps its last result; a
	// disk that is gone is dropped.
	disks := map[string]Disk{}
	for _, d := range devices {
		if disk, ok := read[d.key()]; ok {
			disks[d.key()] = disk
		} else if disk, ok := r.disks[d.key()]; ok {
			disks[d.key()] = disk
		}
	}
	r.disks = disks
	r.readAt = now
	r.reading = false
}

func (r *Reader) scan(ctx context.Context) ([]device, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := r.run(ctx, "--scan-open", "--json")
	devices, parseErr := parseScan(out)
	if parseErr != nil {
		return nil, errors.Join(err, parseErr)
	}
	return devices, nil
}

func (r *Reader) readDisk(ctx context.Context, d device) (Disk, bool) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// smartctl exits with a status other than 0 also when it read the disk
	// and found a problem, so its output counts, not its exit status.
	out, _ := r.run(ctx, "--json", "--all", "--nocheck=standby", "--device="+d.kind, d.name)
	disk, ok := parseDisk(out)
	disk.Name = d.name
	if strings.Contains(d.kind, ",") {
		disk.Name += " " + d.kind
	}
	return disk, ok
}

// validName and validKind match the device names and types smartctl lists,
// so nothing else is ever passed to it as an argument.
var (
	validName = regexp.MustCompile(`^/dev/[A-Za-z0-9_/.:-]+$`)
	validKind = regexp.MustCompile(`^[a-z0-9][a-z0-9_,+-]*$`)
)

type scanOutput struct {
	Devices []struct {
		Name      string `json:"name"`
		Type      string `json:"type"`
		OpenError string `json:"open_error"`
	} `json:"devices"`
}

// parseScan reads the output of smartctl --scan-open --json. Devices that
// could not be opened are left out.
func parseScan(out []byte) ([]device, error) {
	var scan scanOutput
	if err := json.Unmarshal(out, &scan); err != nil {
		return nil, err
	}
	devices := []device{}
	seen := map[string]bool{}
	for _, d := range scan.Devices {
		found := device{name: d.Name, kind: d.Type}
		if d.OpenError != "" || !validName.MatchString(d.Name) || !validKind.MatchString(d.Type) || seen[found.key()] {
			continue
		}
		seen[found.key()] = true
		devices = append(devices, found)
	}
	return devices, nil
}

type diskOutput struct {
	Smartctl struct {
		ExitStatus int `json:"exit_status"`
	} `json:"smartctl"`
	Device      *struct{} `json:"device"`
	ModelName   string    `json:"model_name"`
	ModelFamily string    `json:"model_family"`
	ScsiModel   string    `json:"scsi_model_name"`
	SmartStatus *struct {
		Passed bool `json:"passed"`
	} `json:"smart_status"`
	Temperature struct {
		Current *float64 `json:"current"`
	} `json:"temperature"`
	PowerOnTime struct {
		Hours *float64 `json:"hours"`
	} `json:"power_on_time"`
	ATAAttributes struct {
		Table []struct {
			ID  int `json:"id"`
			Raw struct {
				Value float64 `json:"value"`
			} `json:"raw"`
		} `json:"table"`
	} `json:"ata_smart_attributes"`
	SCSIGrownDefects *float64 `json:"scsi_grown_defect_list"`
	NVMeLog          *struct {
		MediaErrors    *float64 `json:"media_errors"`
		PercentageUsed *float64 `json:"percentage_used"`
	} `json:"nvme_smart_health_information_log"`
}

// reallocatedSectors is the id of the ATA attribute Reallocated_Sector_Ct.
const reallocatedSectors = 5

// parseDisk reads the output of smartctl --json --all. It returns false when
// smartctl read nothing: the disk could not be opened or was asleep.
func parseDisk(out []byte) (Disk, bool) {
	var o diskOutput
	if err := json.Unmarshal(out, &o); err != nil || o.Device == nil ||
		o.Smartctl.ExitStatus&(exitCommandLine|exitOpenOrSleep) != 0 {
		return Disk{}, false
	}
	disk := Disk{
		Model:              firstOf(o.ModelName, o.ScsiModel, o.ModelFamily),
		Celsius:            o.Temperature.Current,
		PowerOnHours:       o.PowerOnTime.Hours,
		ReallocatedSectors: o.SCSIGrownDefects,
	}
	if o.SmartStatus != nil {
		passed := o.SmartStatus.Passed
		disk.Passed = &passed
	}
	for _, attribute := range o.ATAAttributes.Table {
		if attribute.ID == reallocatedSectors {
			value := attribute.Raw.Value
			disk.ReallocatedSectors = &value
		}
	}
	if o.NVMeLog != nil {
		disk.MediaErrors = o.NVMeLog.MediaErrors
		disk.PercentageUsed = o.NVMeLog.PercentageUsed
	}
	return disk, true
}

func firstOf(texts ...string) string {
	for _, text := range texts {
		if text = strings.TrimSpace(text); text != "" {
			return text
		}
	}
	return ""
}
