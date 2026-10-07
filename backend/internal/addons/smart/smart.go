// Package smart reads the health of the machine's disks for the smart
// add-on, straight from the disks with the commands the operating system
// passes to them, without any program to install: whether the disk's own
// SMART check passes, its temperature, power-on hours, reallocated sectors or
// media errors and, for NVMe, how much of its rated wear is used.
//
// SMART reads cost time and would wake disks that sleep, so it reads every
// ReadInterval, a SATA disk only when it read or wrote anything since its
// last read or that read is a day old, and reports the last result in
// between. Before anything else it asks a SATA disk whether it sleeps and,
// on Windows, first asks Windows whether it has switched any disk off; it
// leaves a disk alone if so. It only sends commands that read; nothing in
// here changes the machine.
package smart

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

const (
	// ReadInterval is how often each disk is read, as often as smartd does by
	// default. A read counts as use of the disk, so reading more often than
	// the system switches an unused disk off would keep it from ever doing so.
	ReadInterval = 30 * time.Minute
	// ScanInterval is how often the list of disks is read again, for disks
	// plugged in or removed.
	ScanInterval = time.Hour
	// asleepLogAfter is how long a disk may sleep at every read before that
	// is logged, as some disks or their controllers always answer that they
	// sleep and would then never be shown without a word.
	asleepLogAfter = 24 * time.Hour
	// idleReadAfter is how old the last read of a disk without use may get
	// before it is read anyway, so a check that starts failing on a disk
	// that spins without being used still shows.
	idleReadAfter = 24 * time.Hour
)

// Disk is what was read of one disk. A value that the disk does not report
// is nil.
type Disk struct {
	// Name is how the system calls the disk, such as "sda", "nvme0" or
	// "Disk 0", and Model what the disk calls itself.
	Name  string
	Model string
	// Serial is the disk's serial number, which stays with the disk, unlike
	// its name, which can go to another disk at the next start.
	Serial string
	// Passed is whether the disk's own overall SMART check passes.
	Passed             *bool
	Celsius            *float64
	PowerOnHours       *float64
	ReallocatedSectors *float64
	MediaErrors        *float64
	PercentageUsed     *float64
	// Unreadable is set for a disk that was read before but cannot be read
	// now, which may be failing. It has no values then.
	Unreadable bool
}

// device is a disk to read.
type device struct {
	// name is how the system calls it, path what is opened to read it.
	name, path string
	// nvme tells NVMe disks, read through their health log, from SATA disks,
	// read through ATA commands.
	nvme bool
	// model is what the disk calls itself and serial its serial number,
	// when the list already tells.
	model, serial string
	// blocks are the block devices of the disk in /sys/block on Linux,
	// whose counts of reads and writes tell whether it was used: the disk
	// itself, or each namespace of an NVMe controller.
	blocks []string //nolint:unused,nolintlint // only Linux reads it, so the linter of the other systems finds it unused
}

// errAsleep is returned for a disk that sleeps, which is not woken.
var errAsleep = errors.New("the disk sleeps")

// source lists and reads the disks of one operating system.
type source interface {
	list() ([]device, error)
	// read reads one disk, or returns errAsleep for a disk that sleeps.
	read(d device) (Disk, error)
	// ioCount returns how many reads and writes the system has sent to the
	// disk since it started, without the add-on's own commands, which are
	// not counted, or false where that is not known. Asking does not wake
	// the disk.
	ioCount(d device) (uint64, bool)
}

// ioCount is a disk's count of reads and writes at its last read, and the
// time of that read.
type ioCount struct {
	count uint64
	at    time.Time
}

// Reader reads the disks' health in the background and returns the last
// result.
type Reader struct {
	src source

	mu        sync.Mutex
	reading   bool
	scannedAt time.Time
	readAt    time.Time
	devices   []device
	disks     map[string]Disk
	// ioCounts holds each disk's count of reads and writes (see
	// source.ioCount) when it was read last, and when that was, so a disk
	// that was not used since is not read again before idleReadAfter:
	// reading it could keep it from switching off when its own timer is
	// longer than ReadInterval, or when it starts its timer again at each
	// SMART command.
	ioCounts map[string]ioCount
	// warned holds the disks whose failed read was logged, so a disk that
	// cannot be read is logged once, not every ReadInterval.
	warned map[string]bool
	// asleepSince holds when each disk that was not read yet was first
	// found asleep, and asleepLogged the ones that was logged for.
	asleepSince  map[string]time.Time
	asleepLogged map[string]bool
}

// NewReader returns a Reader of this machine's disks, or nil where the
// add-on cannot read them, which it logs.
func NewReader() *Reader {
	src := newSource()
	if src == nil {
		slog.Warn("reading the disks' health is not supported on this system, so the add-on reports nothing")
		return nil
	}
	return newReader(src)
}

func newReader(src source) *Reader {
	return &Reader{
		src: src, disks: map[string]Disk{}, ioCounts: map[string]ioCount{}, warned: map[string]bool{},
		asleepSince: map[string]time.Time{}, asleepLogged: map[string]bool{},
	}
}

// Read returns the disks read last, in the order they are listed, and when
// a read is due starts it in the background, so a slow disk never holds up
// the report. A nil Reader returns nothing.
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
		if disk, ok := r.disks[d.path]; ok {
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
	// Only refresh changes these maps, and it replaces them, so they can be
	// read without the lock.
	last, lastIOCounts := r.disks, r.ioCounts
	r.mu.Unlock()

	scanned := false
	if scan {
		found, err := r.src.list()
		if err != nil {
			slog.Warn("list the disks", "error", err)
		} else {
			devices, scanned = found, true
		}
	}
	read := map[string]Disk{}
	ioCounts := map[string]ioCount{}
	failed := map[string]error{}
	asleep := map[string]bool{}
	for _, d := range devices {
		if ctx.Err() != nil {
			break
		}
		// Only SATA disks are left alone without use: reading one can spin
		// it up or keep it from switching off. NVMe disks are read every
		// time, so their temperature and hours keep going in the history.
		var count uint64
		var counted bool
		if !d.nvme {
			count, counted = r.src.ioCount(d)
		}
		if counted && !last[d.path].Unreadable {
			if before, ok := lastIOCounts[d.path]; ok && before.count == count && now.Sub(before.at) < idleReadAfter {
				continue
			}
		}
		disk, err := r.src.read(d)
		switch {
		case err == nil:
			disk.Name = d.name
			if disk.Model == "" {
				disk.Model = d.model
			}
			if disk.Serial == "" {
				disk.Serial = d.serial
			}
			read[d.path] = disk
			if counted {
				ioCounts[d.path] = ioCount{count: count, at: now}
			}
		case errors.Is(err, errAsleep):
			asleep[d.path] = true
		default:
			failed[d.path] = err
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	// A failed scan is tried again at the next read.
	if scanned {
		r.scannedAt = now
	}
	r.devices = devices
	// A disk that was asleep or not used keeps its last result. One that
	// could not be read, which may be failing, shows that it cannot be read
	// in place of its values, rather than still showing a check it passed
	// before, and one that is gone is dropped.
	disks := map[string]Disk{}
	for _, d := range devices {
		disk, ok := read[d.path]
		switch err := failed[d.path]; {
		case ok:
			disks[d.path] = disk
			delete(r.warned, d.path)
			delete(r.asleepSince, d.path)
			delete(r.asleepLogged, d.path)
		case err != nil:
			if !r.warned[d.path] {
				slog.Warn("read the disk's health", "disk", d.name, "error", err)
				r.warned[d.path] = true
			}
			if before, ok := r.disks[d.path]; ok {
				disks[d.path] = Disk{Name: d.name, Model: before.Model, Serial: before.Serial, Unreadable: true}
			}
		default:
			if before, ok := r.disks[d.path]; ok {
				disks[d.path] = before
			}
			if count, ok := r.ioCounts[d.path]; ok {
				ioCounts[d.path] = count
			}
		}
		if _, shown := disks[d.path]; asleep[d.path] && !shown {
			r.logAsleep(d, now)
		}
	}
	r.disks = disks
	r.ioCounts = ioCounts
	r.readAt = now
	r.reading = false
}

// logAsleep logs once that a disk that was never read has been asleep at
// every read for asleepLogAfter; r.mu is held.
func (r *Reader) logAsleep(d device, now time.Time) {
	since, ok := r.asleepSince[d.path]
	if !ok {
		r.asleepSince[d.path] = now
		return
	}
	if now.Sub(since) >= asleepLogAfter && !r.asleepLogged[d.path] {
		slog.Info("the disk has been asleep at every read, so its health is not shown; some disks or their controllers always answer that they sleep",
			"disk", d.name, "since", since)
		r.asleepLogged[d.path] = true
	}
}
