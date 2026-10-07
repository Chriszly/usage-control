// Package smart reads the health of the machine's disks for the smart
// add-on, straight from the disks with the commands the operating system
// passes to them, without any program to install: whether the disk's own
// SMART check passes, its temperature, power-on hours, reallocated sectors or
// media errors and, for NVMe, how much of its rated wear is used.
//
// SMART reads cost time and would wake disks that sleep, so it reads every
// ReadInterval, asks a SATA disk whether it sleeps before anything else and
// leaves it alone if it does, and reports the last result in between. It
// only sends commands that read; nothing in here changes the machine.
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
}

// errAsleep is returned for a disk that sleeps, which is not woken.
var errAsleep = errors.New("the disk sleeps")

// source lists and reads the disks of one operating system.
type source interface {
	list() ([]device, error)
	// read reads one disk, or returns errAsleep for a disk that sleeps.
	read(d device) (Disk, error)
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
	// warned holds the disks whose failed read was logged, so a disk that
	// cannot be read is logged once, not every ReadInterval.
	warned map[string]bool
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
	return &Reader{src: src, disks: map[string]Disk{}, warned: map[string]bool{}}
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
	failed := map[string]error{}
	for _, d := range devices {
		if ctx.Err() != nil {
			break
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
		case !errors.Is(err, errAsleep):
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
	// A disk that was asleep keeps its last result; one that could not be
	// read, which may be failing, or that is gone is dropped, rather than
	// still showing a check it passed before.
	disks := map[string]Disk{}
	for _, d := range devices {
		if disk, ok := read[d.path]; ok {
			disks[d.path] = disk
			delete(r.warned, d.path)
		} else if disk, ok := r.disks[d.path]; ok && failed[d.path] == nil {
			disks[d.path] = disk
		}
		if err := failed[d.path]; err != nil && !r.warned[d.path] {
			slog.Warn("read the disk's health", "disk", d.name, "error", err)
			r.warned[d.path] = true
		}
	}
	r.disks = disks
	r.readAt = now
	r.reading = false
}
