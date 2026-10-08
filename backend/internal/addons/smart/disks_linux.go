//go:build linux

package smart

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// linuxSource reads the disks Linux lists in /sys/block: SATA disks through
// SG_IO, which passes ATA commands to them, and NVMe disks through the NVMe
// admin command that reads a log page. Both need root's capabilities
// CAP_SYS_RAWIO and CAP_SYS_ADMIN.
type linuxSource struct {
	// sys and dev are /sys and /dev, other folders in tests.
	sys, dev string
}

func newSource() source {
	return linuxSource{sys: "/sys", dev: "/dev"}
}

var (
	sataName = regexp.MustCompile(`^sd[a-z]+$`)
	nvmeName = regexp.MustCompile(`^(nvme[0-9]+)n[0-9]+$`)
	nvmeCtrl = regexp.MustCompile(`^nvme[0-9]+$`)
)

// list returns the SATA (sd*) and NVMe (nvme*n*) disks in /sys/block,
// without removable, virtual and USB ones. An NVMe disk is read through its
// controller, such as nvme0 for nvme0n1, once for all its namespaces.
func (s linuxSource) list() ([]device, error) {
	block := filepath.Join(s.sys, "block")
	entries, err := os.ReadDir(block)
	if err != nil {
		return nil, err
	}
	devices := []device{}
	seen := map[string]int{}
	for _, entry := range entries {
		name := entry.Name()
		match := nvmeName.FindStringSubmatch(name)
		if !sataName.MatchString(name) && match == nil {
			continue
		}
		if s.text(filepath.Join(block, name, "removable")) == "1" {
			continue
		}
		// Virtual disks such as loop devices sit below /devices/virtual, as
		// do NVMe namespaces with native multipath, which are real disks.
		target, err := filepath.EvalSymlinks(filepath.Join(block, name))
		if err != nil || (strings.Contains(target, "/devices/virtual/") && !strings.Contains(target, "/nvme-subsystem/")) {
			continue
		}
		if onUSB(target) {
			continue
		}
		d := device{name: name, path: filepath.Join(s.dev, name), blocks: []string{name}}
		if match != nil {
			// The kernel keeps the model and serial number the controller
			// tells in its Identify Controller data.
			ctrl := s.controller(filepath.Join(block, name, "device"), match[1])
			d = device{
				name: ctrl, path: filepath.Join(s.dev, ctrl), nvme: true,
				model:  s.text(filepath.Join(s.sys, "class", "nvme", ctrl, "model")),
				serial: s.text(filepath.Join(s.sys, "class", "nvme", ctrl, "serial")),
				blocks: []string{name},
			}
		} else {
			d.model = s.text(filepath.Join(block, name, "device", "model"))
		}
		if i, ok := seen[d.path]; ok {
			devices[i].blocks = append(devices[i].blocks, name)
		} else {
			seen[d.path] = len(devices)
			devices = append(devices, d)
		}
	}
	return devices, nil
}

// ioCount adds up the reads and writes completed on the disk's block
// devices, the first and fifth number of /sys/block/<name>/stat. Commands
// passed through to the disk, as the add-on sends, are not counted there.
// A block device whose counting is switched off (queue/iostats 0) keeps the
// same numbers whatever it does, so the disk then counts as not counted.
func (s linuxSource) ioCount(d device) (uint64, bool) {
	var count uint64
	for _, block := range d.blocks {
		if s.text(filepath.Join(s.sys, "block", block, "queue", "iostats")) == "0" {
			return 0, false
		}
		n, ok := parseBlockStat(s.text(filepath.Join(s.sys, "block", block, "stat")))
		if !ok {
			return 0, false
		}
		count += n
	}
	return count, len(d.blocks) > 0
}

// parseBlockStat returns the reads and writes completed in the text of a
// block device's stat file.
func parseBlockStat(text string) (uint64, bool) {
	fields := strings.Fields(text)
	if len(fields) < 5 {
		return 0, false
	}
	reads, err1 := strconv.ParseUint(fields[0], 10, 64)
	writes, err2 := strconv.ParseUint(fields[4], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	return reads + writes, true
}

// onUSB tells whether a device in /sys/devices hangs off USB, such as
// .../usb2/2-1/2-1:1.0/host0/...: its path has a part that starts with
// "usb". USB disks are left out, also those whose bridge passes ATA
// commands, as some bridges reset the disk when they get one, which would
// happen at every read.
func onUSB(target string) bool {
	for part := range strings.SplitSeq(target, "/") {
		if strings.HasPrefix(part, "usb") {
			return true
		}
	}
	return false
}

// controller returns the NVMe controller of a namespace from its device
// link: the controller itself, or with native multipath its subsystem,
// whose first controller is taken. guess is taken when neither works.
func (s linuxSource) controller(link, guess string) string {
	target, err := filepath.EvalSymlinks(link)
	if err != nil {
		return guess
	}
	if base := filepath.Base(target); nvmeCtrl.MatchString(base) {
		return base
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return guess
	}
	for _, entry := range entries {
		if nvmeCtrl.MatchString(entry.Name()) {
			return entry.Name()
		}
	}
	return guess
}

// text reads a sysfs file, or "" when it is missing.
func (s linuxSource) text(path string) string {
	b, err := os.ReadFile(path) //nolint:gosec // a file below /sys, named from /sys/block
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (s linuxSource) read(d device) (Disk, error) {
	// Read-only: the commands sent only read, and Linux lets the
	// capabilities send them through a disk opened read-only.
	fd, err := unix.Open(d.path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return Disk{}, err
	}
	defer unix.Close(fd) //nolint:errcheck // opened read-only
	if d.nvme {
		log, err := nvmeGetLogPage(fd, nvmeHealthLog, nvmeHealthLogSize)
		if err != nil {
			return Disk{}, err
		}
		return parseNVMeHealth(log)
	}
	return readATA(func(c ataCommand) (ataResult, []byte, error) { return sgATA(fd, c) })
}

// sgIOHdr is struct sg_io_hdr of <scsi/sg.h>.
type sgIOHdr struct {
	interfaceID    int32
	dxferDirection int32
	cmdLen         uint8
	mxSbLen        uint8
	iovecCount     uint16
	dxferLen       uint32
	dxferp         unsafe.Pointer
	cmdp           unsafe.Pointer
	sbp            unsafe.Pointer
	timeout        uint32
	flags          uint32
	packID         int32
	usrPtr         unsafe.Pointer
	status         uint8
	maskedStatus   uint8
	msgStatus      uint8
	sbLenWr        uint8
	hostStatus     uint16
	driverStatus   uint16
	resid          int32
	duration       uint32
	info           uint32
}

const (
	sgIO          = 0x2285
	sgDxferNone   = -1
	sgDxferFromDv = -3
	// sgTimeout is how long one command may take, in milliseconds.
	sgTimeout = 15000
	// scsiCheckCondition is the SCSI status that comes with sense data.
	scsiCheckCondition = 0x02
	// sgDriverMask is the driver's own part of the driver status, and
	// sgDriverSense its flag that sense data came (DRIVER_SENSE).
	sgDriverMask  = 0x0F
	sgDriverSense = 0x08
)

// errNoAnswer is returned when a disk answered a command without the
// registers that hold the answer.
var errNoAnswer = errors.New("the disk did not return its registers")

// sgATA sends an ATA command to a SATA disk as ATA PASS-THROUGH (16) through
// SG_IO, and returns the registers it answered with (for a command without
// data) or the sector it returned.
//
//nolint:gosec // SG_IO takes pointers, which need unsafe.
func sgATA(fd int, c ataCommand) (ataResult, []byte, error) {
	cdb := c.cdb()
	sense := make([]byte, 32)
	hdr := sgIOHdr{
		interfaceID:    'S',
		dxferDirection: sgDxferNone,
		cmdLen:         uint8(len(cdb)),
		mxSbLen:        uint8(len(sense)),
		cmdp:           unsafe.Pointer(&cdb[0]),
		sbp:            unsafe.Pointer(&sense[0]),
		timeout:        sgTimeout,
	}
	var data []byte
	if c.dataIn {
		data = make([]byte, 512)
		hdr.dxferDirection = sgDxferFromDv
		hdr.dxferLen = uint32(len(data))
		hdr.dxferp = unsafe.Pointer(&data[0])
	}
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), sgIO, uintptr(unsafe.Pointer(&hdr)))
	runtime.KeepAlive(cdb)
	runtime.KeepAlive(sense)
	runtime.KeepAlive(data)
	if errno != 0 {
		return ataResult{}, nil, errno
	}
	return sgAnswer(c, &hdr, sense, data)
}

// Sense keys of SCSI sense data that report no error: NO SENSE, and
// RECOVERED ERROR, which a disk or a bridge in front of it may send with an
// answer that is complete, and which smartctl takes as no error too.
const (
	senseNoSense        = 0x00
	senseRecoveredError = 0x01
)

// senseKey returns the sense key of fixed- or descriptor-format sense data,
// or false when there is none.
func senseKey(sense []byte) (byte, bool) {
	if len(sense) < 3 {
		return 0, false
	}
	switch sense[0] & 0x7F {
	case 0x70, 0x71:
		return sense[2] & 0x0F, true
	case 0x72, 0x73:
		return sense[1] & 0x0F, true
	}
	return 0, false
}

// noError tells whether sense data reports no error (see senseNoSense).
func noError(sense []byte) bool {
	key, ok := senseKey(sense)
	return ok && (key == senseNoSense || key == senseRecoveredError)
}

// sgAnswer reads what SG_IO returned for the ATA command c in hdr, with the
// sense data and the data buffer it was given: the registers, or the sector.
// A sector the disk did not return in full is an error, as an empty buffer
// would read as a disk without SMART.
func sgAnswer(c ataCommand, hdr *sgIOHdr, sense, data []byte) (ataResult, []byte, error) {
	if hdr.hostStatus != 0 {
		return ataResult{}, nil, fmt.Errorf("SG_IO host status %#x", hdr.hostStatus)
	}
	// Of the driver status, only the flag that sense data came is no error.
	if driver := hdr.driverStatus & sgDriverMask; driver != 0 && driver != sgDriverSense {
		return ataResult{}, nil, fmt.Errorf("SG_IO driver status %#x", hdr.driverStatus)
	}
	written := sense[:min(int(hdr.sbLenWr), len(sense))]
	result, ok := parseATASense(written)
	if ok && result.status&ataStatusError != 0 {
		return ataResult{}, nil, fmt.Errorf("%w %#x (error %#x)", errRefused, c.command, result.err)
	}
	switch {
	// Some bridges send a sector with RECOVERED ERROR, which smartctl
	// takes as read too.
	case c.dataIn && hdr.status != 0 && (hdr.status != scsiCheckCondition || !noError(written)):
		return ataResult{}, nil, fmt.Errorf("SCSI status %#x", hdr.status)
	case c.dataIn && hdr.resid != 0:
		return ataResult{}, nil, fmt.Errorf("the disk returned %d of %d bytes", len(data)-int(hdr.resid), len(data))
	case c.dataIn:
		return result, data, nil
	case !ok && hdr.status == scsiCheckCondition:
		return ataResult{}, nil, fmt.Errorf("SCSI status %#x without the registers", hdr.status)
	case !ok:
		return ataResult{}, nil, errNoAnswer
	}
	return result, nil, nil
}

// nvmeAdminCmd is struct nvme_passthru_cmd of <linux/nvme_ioctl.h>.
type nvmeAdminCmd struct {
	opcode      uint8
	flags       uint8
	rsvd1       uint16
	nsid        uint32
	cdw2        uint32
	cdw3        uint32
	metadata    uint64
	addr        uint64
	metadataLen uint32
	dataLen     uint32
	cdw10       uint32
	cdw11       uint32
	cdw12       uint32
	cdw13       uint32
	cdw14       uint32
	cdw15       uint32
	timeoutMs   uint32
	result      uint32
}

const (
	// nvmeIoctlAdminCmd is NVME_IOCTL_ADMIN_CMD, _IOWR('N', 0x41, struct
	// nvme_passthru_cmd).
	nvmeIoctlAdminCmd = 0xC0484E41
	nvmeGetLogPageOp  = 0x02
	nvmeAllNamespaces = 0xFFFFFFFF
)

// nvmeGetLogPage reads a log page of an NVMe controller with the admin
// command Get Log Page.
//
//nolint:gosec // the NVMe ioctl takes pointers, which need unsafe.
func nvmeGetLogPage(fd int, id uint8, size int) ([]byte, error) {
	data := make([]byte, size)
	cmd := nvmeAdminCmd{
		opcode:  nvmeGetLogPageOp,
		nsid:    nvmeAllNamespaces,
		addr:    uint64(uintptr(unsafe.Pointer(&data[0]))),
		dataLen: uint32(size),
		// The number of dwords minus 1, and the log page.
		cdw10:     uint32(size/4-1)<<16 | uint32(id),
		timeoutMs: sgTimeout,
	}
	r, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), nvmeIoctlAdminCmd, uintptr(unsafe.Pointer(&cmd)))
	runtime.KeepAlive(data)
	if errno != 0 {
		return nil, errno
	}
	if r != 0 {
		return nil, fmt.Errorf("NVMe status %#x", r)
	}
	return data, nil
}
