package metrics

import (
	"strconv"
	"strings"
	"time"
)

// This file turns the disk counters of Linux into numbers. It has no build
// constraint so its tests run on every OS.

// ioCounters is what a disk did since the machine booted: the bytes read and
// written, the number of reads and writes, and in milliseconds how long they
// took and how long the disk was busy. Windows reports no times, so
// hasTimes is false there.
type ioCounters struct {
	read       uint64
	written    uint64
	operations uint64
	waitMs     uint64
	busyMs     uint64
	hasTimes   bool
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
		// major minor name reads merged sectors-read ms-reading
		// writes merged sectors-written ms-writing in-flight ms-busy ...
		fields := strings.Fields(line)
		if len(fields) < 13 {
			continue
		}
		device, ok := parseDeviceNumber(fields[0] + ":" + fields[1])
		if !ok {
			continue
		}
		var n [13]uint64
		valid := true
		for _, i := range []int{3, 5, 6, 7, 9, 10, 12} {
			value, err := strconv.ParseUint(fields[i], 10, 64)
			if err != nil {
				valid = false
				break
			}
			n[i] = value
		}
		if !valid {
			continue
		}
		stats[device] = ioCounters{
			read:       n[5] * sectorBytes,
			written:    n[9] * sectorBytes,
			operations: n[3] + n[7],
			waitMs:     n[6] + n[10],
			busyMs:     n[12],
			hasTimes:   true,
		}
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

// diskActivity turns two readings of the disk counters into speeds,
// operations per second, how busy the disk was and how long an operation
// took on average. A disk without counters has none of them (nil), and busy
// and latency are left out where the system reports no times. The first
// reading, and counters that went down, count as no activity.
func diskActivity(previous, current map[string]ioCounters, elapsed time.Duration, disks []Disk) {
	for i, d := range disks {
		now, ok := current[d.Path]
		if !ok {
			continue
		}
		var read, write, operations, busy, latency float64
		if before, ok := previous[d.Path]; ok && elapsed > 0 {
			read = perSecond(before.read, now.read, elapsed)
			write = perSecond(before.written, now.written, elapsed)
			operations = perSecond(before.operations, now.operations, elapsed)
			if now.busyMs >= before.busyMs {
				busy = min(100, float64(now.busyMs-before.busyMs)/float64(elapsed.Milliseconds())*100)
			}
			if now.operations > before.operations && now.waitMs >= before.waitMs {
				latency = float64(now.waitMs-before.waitMs) / float64(now.operations-before.operations)
			}
		}
		disks[i].ReadBytesPerSecond, disks[i].WriteBytesPerSecond = &read, &write
		disks[i].OperationsPerSecond = &operations
		if now.hasTimes {
			disks[i].BusyPercent, disks[i].LatencyMs = &busy, &latency
		}
	}
}
