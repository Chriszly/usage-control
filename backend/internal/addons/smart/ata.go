package smart

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// ataCommand is the registers of an ATA command (ATA/ATAPI Command Set).
type ataCommand struct {
	command, features, count, lbaLow, lbaMid, lbaHigh byte
	// dataIn is whether the disk answers with one 512-byte sector.
	dataIn bool
}

// The ATA commands the add-on sends. All of them only read.
var (
	// ataCheckPowerMode asks whether the disk sleeps, without waking it.
	ataCheckPowerMode = ataCommand{command: 0xE5}
	// ataIdentify reads what the disk is and whether SMART is on.
	ataIdentify = ataCommand{command: 0xEC, count: 1, dataIn: true}
	// ataSMARTReadData reads the SMART attributes.
	ataSMARTReadData = ataCommand{command: 0xB0, features: 0xD0, count: 1, lbaMid: 0x4F, lbaHigh: 0xC2, dataIn: true}
	// ataSMARTReturnStatus asks whether the disk passes its own check.
	ataSMARTReturnStatus = ataCommand{command: 0xB0, features: 0xDA, lbaMid: 0x4F, lbaHigh: 0xC2}
)

// ataResult is the registers the disk answers a command with.
type ataResult struct {
	status, err, count, lbaMid, lbaHigh byte
}

// ataStatusError is the ERR bit of the status register.
const ataStatusError = 0x01

// cdb returns the command as an ATA PASS-THROUGH (16) SCSI command (SAT,
// SCSI / ATA Translation), as Linux passes it to SATA disks. A command
// without data asks for the registers back (CK_COND), as they hold the
// answer.
func (c ataCommand) cdb() []byte {
	cdb := make([]byte, 16)
	cdb[0] = 0x85
	if c.dataIn {
		cdb[1] = 4 << 1 // protocol PIO data-in
		cdb[2] = 0x0E   // from the disk, length in sectors, in the count register
	} else {
		cdb[1] = 3 << 1 // protocol non-data
		cdb[2] = 0x20   // CK_COND
	}
	cdb[4] = c.features
	cdb[6] = c.count
	cdb[8] = c.lbaLow
	cdb[10] = c.lbaMid
	cdb[12] = c.lbaHigh
	cdb[14] = c.command
	return cdb
}

// parseATASense reads the registers a SATA disk answered with from the
// sense data of an ATA PASS-THROUGH command with CK_COND: in the ATA Status
// Return descriptor of descriptor-format sense data, or in the fields of
// fixed-format sense data. It returns false when the sense data holds none.
func parseATASense(sense []byte) (ataResult, bool) {
	if len(sense) < 14 {
		return ataResult{}, false
	}
	switch sense[0] & 0x7F {
	case 0x72, 0x73:
		end := min(len(sense), 8+int(sense[7]))
		for i := 8; i+1 < end; i += 2 + int(sense[i+1]) {
			if d := sense[i:min(end, i+2+int(sense[i+1]))]; d[0] == 0x09 && len(d) >= 14 {
				return ataResult{err: d[3], count: d[5], lbaMid: d[9], lbaHigh: d[11], status: d[13]}, true
			}
		}
	case 0x70, 0x71:
		// "ATA PASS-THROUGH INFORMATION AVAILABLE"
		if sense[12] == 0x00 && sense[13] == 0x1D {
			return ataResult{err: sense[3], status: sense[4], count: sense[6], lbaMid: sense[10], lbaHigh: sense[11]}, true
		}
	}
	return ataResult{}, false
}

// asleep reads the answer to CHECK POWER MODE: standby, or a disk whose
// spindle is spun down while it serves from its non-volatile cache.
func asleep(r ataResult) bool {
	return r.count == 0x00 || r.count == 0x01 || r.count == 0x40
}

// smartPassed reads the answer to SMART RETURN STATUS, or nil when it is
// neither of the two answers the standard allows.
func smartPassed(r ataResult) *bool {
	var passed bool
	switch {
	case r.lbaMid == 0x4F && r.lbaHigh == 0xC2:
		passed = true
	case r.lbaMid == 0xF4 && r.lbaHigh == 0x2C:
		passed = false
	default:
		return nil
	}
	return &passed
}

// errChecksum is returned for a sector whose checksum is wrong.
var errChecksum = errors.New("the checksum of the sector is wrong")

// checksum checks a sector whose last byte makes all 512 add up to 0.
func checksum(sector []byte) error {
	if len(sector) != 512 {
		return fmt.Errorf("the sector has %d bytes, not 512", len(sector))
	}
	var sum byte
	for _, b := range sector {
		sum += b
	}
	if sum != 0 {
		return errChecksum
	}
	return nil
}

// parseIdentify reads the model, the serial number and whether SMART is
// supported and on from the answer to IDENTIFY DEVICE.
func parseIdentify(sector []byte) (model, serial string, smart bool, err error) {
	if len(sector) != 512 {
		return "", "", false, fmt.Errorf("the sector has %d bytes, not 512", len(sector))
	}
	// The checksum is only there when word 255 starts with the signature A5h.
	if sector[510] == 0xA5 {
		if err := checksum(sector); err != nil {
			return "", "", false, err
		}
	}
	word := func(i int) uint16 { return binary.LittleEndian.Uint16(sector[2*i:]) }
	supported := word(82) != 0xFFFF && word(82)&1 != 0
	enabled := word(85) != 0xFFFF && word(85)&1 != 0
	return ataText(sector, 27, 46), ataText(sector, 10, 19), supported && enabled, nil
}

// ataText reads the text in words first to last of IDENTIFY DEVICE, two
// characters each, the first in the high byte.
func ataText(sector []byte, first, last int) string {
	text := make([]byte, 0, 2*(last-first+1))
	for i := first; i <= last; i++ {
		text = append(text, sector[2*i+1], sector[2*i])
	}
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7E {
			return -1
		}
		return r
	}, string(text)))
}

// The ATA SMART attributes the add-on reads.
const (
	attrReallocatedSectors = 5
	attrPowerOnHours       = 9
	attrAirflowTemperature = 190
	attrTemperature        = 194
)

// parseSMARTData reads the reallocated sectors, power-on hours and
// temperature from the answer to SMART READ DATA: up to 30 attributes of 12
// bytes from byte 2, each an id, flags, the normalized values and a 6-byte
// raw value. Of the raw value, counters use the low 4 bytes, as some disks
// put other numbers in the high ones, and temperatures the lowest byte, as
// many disks put the lowest and highest temperature in the others.
func parseSMARTData(sector []byte) (Disk, error) {
	if err := checksum(sector); err != nil {
		return Disk{}, err
	}
	var disk Disk
	var airflow *float64
	for i := 2; i+12 <= 362; i += 12 {
		raw := sector[i+5 : i+11]
		switch sector[i] {
		case attrReallocatedSectors:
			disk.ReallocatedSectors = number(float64(binary.LittleEndian.Uint32(raw)))
		case attrPowerOnHours:
			disk.PowerOnHours = number(float64(binary.LittleEndian.Uint32(raw)))
		case attrTemperature:
			if raw[0] != 0 {
				disk.Celsius = number(float64(raw[0]))
			}
		case attrAirflowTemperature:
			if raw[0] != 0 {
				airflow = number(float64(raw[0]))
			}
		}
	}
	if disk.Celsius == nil {
		disk.Celsius = airflow
	}
	return disk, nil
}

// readATA reads a SATA disk with send, which sends one ATA command: it asks
// whether the disk sleeps first and leaves it alone if it does, then reads
// its model and serial number, its SMART check and its SMART attributes. A
// disk whose power mode cannot be read is not read either, as it might sleep.
func readATA(send func(ataCommand) (ataResult, []byte, error)) (Disk, error) {
	power, _, err := send(ataCheckPowerMode)
	if err != nil {
		return Disk{}, fmt.Errorf("check the power mode: %w", err)
	}
	if asleep(power) {
		return Disk{}, errAsleep
	}
	_, sector, err := send(ataIdentify)
	if err != nil {
		return Disk{}, fmt.Errorf("identify: %w", err)
	}
	model, serial, smart, err := parseIdentify(sector)
	if err != nil {
		return Disk{}, fmt.Errorf("identify: %w", err)
	}
	if !smart {
		return Disk{Model: model, Serial: serial}, nil
	}
	_, sector, err = send(ataSMARTReadData)
	if err != nil {
		return Disk{}, fmt.Errorf("read the SMART data: %w", err)
	}
	disk, err := parseSMARTData(sector)
	if err != nil {
		return Disk{}, fmt.Errorf("read the SMART data: %w", err)
	}
	disk.Model, disk.Serial = model, serial
	if status, _, err := send(ataSMARTReturnStatus); err == nil {
		disk.Passed = smartPassed(status)
	}
	return disk, nil
}
