//go:build windows

package smart

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// windowsSource reads the disks Windows numbers \\.\PhysicalDrive0 and
// onwards: NVMe disks through the NVMe driver, which reads their health log,
// and SATA disks through the disk driver's SMART commands. Sending these
// needs a disk opened for reading and writing, which only administrators
// and LocalSystem may do; the commands sent only read.
type windowsSource struct{}

func newSource() source {
	return windowsSource{}
}

// maxDrives is how many \\.\PhysicalDriveN are tried. Numbers can have gaps
// when a disk is removed, so a missing one does not end the list.
const maxDrives = 32

const (
	ioctlStorageQueryProperty = 0x002D1400
	ioctlATAPassThrough       = 0x0004D02C
	smartSendDriveCommand     = 0x0007C084
	smartRcvDriveData         = 0x0007C088
)

func open(path string, access uint32) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, err
	}
	return windows.CreateFile(name, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
}

// deviceQuery returns a STORAGE_PROPERTY_QUERY for StorageDeviceProperty.
func deviceQuery() []byte {
	q := make([]byte, 12)
	binary.LittleEndian.PutUint32(q[0:], storageDeviceProperty)
	return q
}

// ioctl sends code with in and returns the first bytes of out it filled.
func ioctl(h windows.Handle, code uint32, in, out []byte) ([]byte, error) {
	var returned uint32
	//nolint:gosec // the buffers hold a few hundred bytes
	err := windows.DeviceIoControl(h, code, &in[0], uint32(len(in)), &out[0], uint32(len(out)), &returned, nil)
	if err != nil {
		return nil, err
	}
	return out[:returned], nil
}

// list returns the fixed SATA and NVMe disks, with their models and serial
// numbers. Asking a disk what it is needs no access to its data and does not
// wake it. USB disks are left out, as some USB bridges reset the disk when
// they get an ATA command passed through.
func (windowsSource) list() ([]device, error) {
	devices := []device{}
	for i := range maxDrives {
		path := fmt.Sprintf(`\\.\PhysicalDrive%d`, i)
		h, err := open(path, 0)
		if err != nil {
			continue
		}
		out, err := ioctl(h, ioctlStorageQueryProperty, deviceQuery(), make([]byte, 1024))
		_ = windows.CloseHandle(h)
		if err != nil {
			continue
		}
		d, err := parseDeviceDescriptor(out)
		if err != nil || d.removable {
			continue
		}
		switch d.bus {
		case busNVMe, busATA, busSATA:
			devices = append(devices, device{
				name: fmt.Sprintf("Disk %d", i), path: path, nvme: d.bus == busNVMe,
				model: d.model, serial: d.serial,
			})
		}
	}
	return devices, nil
}

func (windowsSource) read(d device) (Disk, error) {
	// Opening a disk to read and write may wake it, and spin a SATA disk up,
	// when Windows has switched it off, as its power plan does after a while
	// without use, so that is first asked through a handle that may only ask,
	// as smartctl does.
	if switchedOff(d.path) {
		return Disk{}, errAsleep
	}
	h, err := open(d.path, windows.GENERIC_READ|windows.GENERIC_WRITE)
	if err != nil {
		return Disk{}, err
	}
	defer windows.CloseHandle(h) //nolint:errcheck // nothing was written
	if d.nvme {
		// The answer comes back in the buffer of the question.
		query := nvmeLogQuery(nvmeHealthLog, nvmeHealthLogSize)
		out, err := ioctl(h, ioctlStorageQueryProperty, query, query)
		if err != nil {
			return Disk{}, err
		}
		log, err := nvmeLogFromDescriptor(out, nvmeHealthLogSize)
		if err != nil {
			return Disk{}, err
		}
		return parseNVMeHealth(log)
	}
	return readATA(func(c ataCommand) (ataResult, []byte, error) { return sendATA(h, c) })
}

// sendATA sends an ATA command to a SATA disk: CHECK POWER MODE as ATA pass
// through, as the SMART IOCTLs cannot send it, and the others through
// SMART_RCV_DRIVE_DATA and SMART_SEND_DRIVE_COMMAND, which drivers support
// more widely.
func sendATA(h windows.Handle, c ataCommand) (ataResult, []byte, error) {
	if c == ataCheckPowerMode {
		return checkPowerMode(h)
	}
	code := uint32(smartRcvDriveData)
	if !c.dataIn {
		code = smartSendDriveCommand
	}
	out, err := ioctl(h, code, sendCmdIn(c), make([]byte, sendCmdOutSize(c)))
	if err != nil {
		return ataResult{}, nil, err
	}
	return parseSendCmdOut(c, out)
}

// ataPassThroughEx is ATA_PASS_THROUGH_EX of ntddscsi.h.
type ataPassThroughEx struct {
	length             uint16
	ataFlags           uint16
	pathID             uint8
	targetID           uint8
	lun                uint8
	reservedAsUchar    uint8
	dataTransferLength uint32
	timeOutValue       uint32
	reservedAsUlong    uint32
	dataBufferOffset   uintptr
	previousTaskFile   [8]byte
	currentTaskFile    [8]byte
}

const ataFlagsDrdyRequired = 0x01

var getDevicePowerState = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetDevicePowerState")

// errNoPowerMode is returned when neither way tells whether a disk sleeps.
var errNoPowerMode = errors.New("neither ATA pass through nor GetDevicePowerState tells whether the disk sleeps")

// switchedOff reports whether Windows has switched the disk at path off,
// asked with GetDevicePowerState through a handle without access to the
// disk's data, which does not wake it.
//
//nolint:gosec // kernel32 takes a pointer, which needs unsafe.
func switchedOff(path string) bool {
	h, err := open(path, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h) //nolint:errcheck // only asked
	var on int32
	ret, _, _ := getDevicePowerState.Call(uintptr(h), uintptr(unsafe.Pointer(&on)))
	return ret != 0 && on == 0
}

// checkPowerMode asks a SATA disk whether it sleeps with CHECK POWER MODE as
// ATA pass through, or where the driver does not pass ATA commands, asks
// Windows with GetDevicePowerState. Neither wakes the disk.
//
//nolint:gosec // DeviceIoControl and kernel32 take pointers, which need unsafe.
func checkPowerMode(h windows.Handle) (ataResult, []byte, error) {
	pt := ataPassThroughEx{ataFlags: ataFlagsDrdyRequired, timeOutValue: 15}
	pt.length = uint16(unsafe.Sizeof(pt))
	pt.currentTaskFile[6] = ataCheckPowerMode.command
	var returned uint32
	size := uint32(unsafe.Sizeof(pt))
	err := windows.DeviceIoControl(h, ioctlATAPassThrough, (*byte)(unsafe.Pointer(&pt)), size, (*byte)(unsafe.Pointer(&pt)), size, &returned, nil)
	if err == nil && pt.currentTaskFile[6]&ataStatusError == 0 {
		return ataResult{status: pt.currentTaskFile[6], count: pt.currentTaskFile[1]}, nil, nil
	}
	var on int32
	if ret, _, _ := getDevicePowerState.Call(uintptr(h), uintptr(unsafe.Pointer(&on))); ret == 0 {
		return ataResult{}, nil, errNoPowerMode
	}
	if on == 0 {
		return ataResult{count: 0x00}, nil, nil
	}
	return ataResult{count: 0xFF}, nil, nil
}
