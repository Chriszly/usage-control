package smart

import (
	"encoding/binary"
	"errors"
	"strings"
)

// The byte layouts of the Windows storage IOCTLs the add-on uses
// (ntddstor.h, ntdddisk.h). They are built and read here as bytes, so they
// are tested on every system.

// Bus types of STORAGE_BUS_TYPE.
const (
	busATA  = 0x03
	busUSB  = 0x07
	busSATA = 0x0B
	busNVMe = 0x11
)

// storageDevice is what STORAGE_DEVICE_DESCRIPTOR tells of a disk.
type storageDevice struct {
	model, serial string
	bus           uint32
	removable     bool
}

var errShortDescriptor = errors.New("the storage descriptor is too short")

// parseDeviceDescriptor reads the STORAGE_DEVICE_DESCRIPTOR that
// IOCTL_STORAGE_QUERY_PROPERTY returns for StorageDeviceProperty. The model
// is the vendor and product id, which for SATA and NVMe disks is usually the
// product id alone.
func parseDeviceDescriptor(b []byte) (storageDevice, error) {
	if len(b) < 36 {
		return storageDevice{}, errShortDescriptor
	}
	size := min(len(b), int(binary.LittleEndian.Uint32(b[4:8])))
	// An offset is 0 for a text the disk does not tell; one beyond the
	// descriptor is taken as none too.
	text := func(offset uint32) string {
		if offset == 0 || int64(offset) >= int64(size) {
			return ""
		}
		end := int(offset)
		for end < size && b[end] != 0 {
			end++
		}
		return strings.TrimSpace(string(b[offset:end]))
	}
	vendor := text(binary.LittleEndian.Uint32(b[12:16]))
	product := text(binary.LittleEndian.Uint32(b[16:20]))
	model := product
	if vendor != "" && !strings.HasPrefix(product, vendor) {
		model = strings.TrimSpace(vendor + " " + product)
	}
	return storageDevice{
		model:     model,
		serial:    text(binary.LittleEndian.Uint32(b[24:28])),
		bus:       binary.LittleEndian.Uint32(b[28:32]),
		removable: b[10] != 0,
	}, nil
}

const (
	storageDeviceProperty                 = 0
	storageDeviceProtocolSpecificProperty = 50
	protocolTypeNvme                      = 3
	nvmeDataTypeLogPage                   = 2
	// protocolDataOffset is sizeof(STORAGE_PROTOCOL_SPECIFIC_DATA), where
	// the log starts after it.
	protocolDataOffset = 40
	// queryHeader is the offset of AdditionalParameters in
	// STORAGE_PROPERTY_QUERY, and of ProtocolSpecificData in
	// STORAGE_PROTOCOL_DATA_DESCRIPTOR.
	queryHeader = 8
)

// nvmeLogQuery returns a STORAGE_PROPERTY_QUERY that asks the NVMe driver
// for a log page, in a buffer long enough for the answer, which comes back
// in the same buffer.
func nvmeLogQuery(id, size uint32) []byte {
	q := make([]byte, queryHeader+protocolDataOffset+int(size))
	binary.LittleEndian.PutUint32(q[0:], storageDeviceProtocolSpecificProperty)
	// QueryType PropertyStandardQuery is 0.
	p := q[queryHeader:]
	binary.LittleEndian.PutUint32(p[0:], protocolTypeNvme)
	binary.LittleEndian.PutUint32(p[4:], nvmeDataTypeLogPage)
	binary.LittleEndian.PutUint32(p[8:], id)
	binary.LittleEndian.PutUint32(p[16:], protocolDataOffset)
	binary.LittleEndian.PutUint32(p[20:], size)
	return q
}

var errShortLog = errors.New("the NVMe driver returned less of the log than asked")

// nvmeLogFromDescriptor reads the log out of the
// STORAGE_PROTOCOL_DATA_DESCRIPTOR the NVMe driver answers with.
func nvmeLogFromDescriptor(b []byte, size int) ([]byte, error) {
	if len(b) < queryHeader+protocolDataOffset {
		return nil, errShortLog
	}
	p := b[queryHeader:]
	offset := int(binary.LittleEndian.Uint32(p[16:]))
	length := int(binary.LittleEndian.Uint32(p[20:]))
	if offset < protocolDataOffset || length < size || offset+size > len(p) {
		return nil, errShortLog
	}
	return p[offset : offset+size], nil
}

// diskPerformanceSize is the size of DISK_PERFORMANCE: five 8-byte times
// and byte counts, four 4-byte counts, an 8-byte time, a 4-byte number and
// a name of 8 UTF-16 characters.
const diskPerformanceSize = 88

// parseDiskPerformance reads the reads and writes, ReadCount and WriteCount,
// from the DISK_PERFORMANCE that IOCTL_DISK_PERFORMANCE returns.
func parseDiskPerformance(b []byte) (uint64, bool) {
	if len(b) < 48 {
		return 0, false
	}
	return uint64(binary.LittleEndian.Uint32(b[40:])) + uint64(binary.LittleEndian.Uint32(b[44:])), true
}

// The SMART IOCTLs' SENDCMDINPARAMS and SENDCMDOUTPARAMS: the input is
// cBufferSize, the 8 IDE registers, the drive number and reserved bytes, 32
// bytes up to its buffer; the output is cBufferSize and 12 bytes of
// DRIVERSTATUS, 16 bytes up to its buffer.
const (
	sendCmdInSize   = 32
	sendCmdOutData  = 16
	ideRegsSize     = 8
	driveHeadMaster = 0xA0
)

// sendCmdIn returns the SENDCMDINPARAMS of an ATA command for
// SMART_RCV_DRIVE_DATA or SMART_SEND_DRIVE_COMMAND.
func sendCmdIn(c ataCommand) []byte {
	in := make([]byte, sendCmdInSize)
	if c.dataIn {
		binary.LittleEndian.PutUint32(in[0:], 512)
	}
	regs := in[4:12]
	regs[0] = c.features
	regs[1] = c.count
	regs[2] = c.lbaLow
	regs[3] = c.lbaMid
	regs[4] = c.lbaHigh
	regs[5] = driveHeadMaster
	regs[6] = c.command
	return in
}

// sendCmdOutSize is the size of SENDCMDOUTPARAMS for the answer to c: a
// sector, or the IDE registers.
func sendCmdOutSize(c ataCommand) int {
	if c.dataIn {
		return sendCmdOutData + 512
	}
	return sendCmdOutData + ideRegsSize
}

var errShortAnswer = errors.New("the disk driver answered too little")

var errDriver = errors.New("the disk driver refused the command")

// parseSendCmdOut reads SENDCMDOUTPARAMS: the sector, or the registers the
// disk answered with.
func parseSendCmdOut(c ataCommand, out []byte) (ataResult, []byte, error) {
	if len(out) < sendCmdOutSize(c) {
		return ataResult{}, nil, errShortAnswer
	}
	// DRIVERSTATUS.bDriverError
	if out[4] != 0 {
		return ataResult{}, nil, errDriver
	}
	data := out[sendCmdOutData:sendCmdOutSize(c)]
	if c.dataIn {
		return ataResult{}, data, nil
	}
	return ataResult{err: data[0], count: data[1], lbaMid: data[3], lbaHigh: data[4], status: data[6]}, nil, nil
}
