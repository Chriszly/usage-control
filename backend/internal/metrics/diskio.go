package metrics

import (
	"strconv"
	"strings"
	"time"
)

// This file turns the disk counters of Linux into numbers. It has no build
// constraint so its tests run on every OS.

// ioCounters is the number of bytes read from and written to a disk since the
// machine booted.
type ioCounters struct {
	read    uint64
	written uint64
}

// deviceNumber is the major and minor number Linux gives a block device, such
// as 179:2 for the second partition of a Raspberry Pi's SD card.
type deviceNumber struct {
	major uint32
	minor uint32
}

// sectorBytes is the size of the sectors /proc/diskstats counts in, whatever
// the sector size of the disk.
const sectorBytes = 512

// parseDiskstats reads the counters of each block device from the text of
// /proc/diskstats. Lines that do not have the expected shape are skipped.
func parseDiskstats(text string) map[deviceNumber]ioCounters {
	stats := map[deviceNumber]ioCounters{}
	for line := range strings.Lines(text) {
		// major minor name reads merged sectors-read ms writes merged sectors-written ...
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		device, ok := parseDeviceNumber(fields[0] + ":" + fields[1])
		read, readErr := strconv.ParseUint(fields[5], 10, 64)
		written, writeErr := strconv.ParseUint(fields[9], 10, 64)
		if !ok || readErr != nil || writeErr != nil {
			continue
		}
		stats[device] = ioCounters{read: read * sectorBytes, written: written * sectorBytes}
	}
	return stats
}

// parseMountinfoRoot returns the device the root filesystem is mounted from,
// from the text of /proc/<pid>/mountinfo.
func parseMountinfoRoot(text string) (deviceNumber, bool) {
	for line := range strings.Lines(text) {
		// id parent major:minor root mount-point ...
		fields := strings.Fields(line)
		if len(fields) >= 5 && fields[4] == "/" {
			return parseDeviceNumber(fields[2])
		}
	}
	return deviceNumber{}, false
}

func parseDeviceNumber(text string) (deviceNumber, bool) {
	majorText, minorText, found := strings.Cut(text, ":")
	major, majorErr := strconv.ParseUint(majorText, 10, 32)
	minor, minorErr := strconv.ParseUint(minorText, 10, 32)
	if !found || majorErr != nil || minorErr != nil {
		return deviceNumber{}, false
	}
	return deviceNumber{major: uint32(major), minor: uint32(minor)}, true
}

// diskSpeeds turns two readings of the disk counters into bytes per second.
// A disk without counters has no speed (nil); the first reading, and counters
// that went down, have a speed of 0.
func diskSpeeds(previous, current map[string]ioCounters, elapsed time.Duration, disks []Disk) {
	for i, d := range disks {
		now, ok := current[d.Path]
		if !ok {
			continue
		}
		var read, write float64
		if before, ok := previous[d.Path]; ok && elapsed > 0 {
			read = perSecond(before.read, now.read, elapsed)
			write = perSecond(before.written, now.written, elapsed)
		}
		disks[i].ReadBytesPerSecond, disks[i].WriteBytesPerSecond = &read, &write
	}
}
