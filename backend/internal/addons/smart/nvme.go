package smart

import (
	"encoding/binary"
	"fmt"
	"math"
)

// nvmeHealthLog is the id of the NVMe SMART / Health Information log page,
// and nvmeHealthLogSize its length in bytes.
const (
	nvmeHealthLog     = 0x02
	nvmeHealthLogSize = 512
)

// parseNVMeHealth reads an NVMe SMART / Health Information log page (NVMe
// Base Specification, "SMART / Health Information"). The disk fails its
// check when it raises any critical warning.
func parseNVMeHealth(log []byte) (Disk, error) {
	if len(log) < nvmeHealthLogSize {
		return Disk{}, fmt.Errorf("the NVMe health log has %d bytes, not %d", len(log), nvmeHealthLogSize)
	}
	passed := log[0] == 0
	disk := Disk{
		Passed:         &passed,
		PercentageUsed: number(float64(log[5])),
		PowerOnHours:   number(uint128(log[128:144])),
		MediaErrors:    number(uint128(log[160:176])),
	}
	// The composite temperature in kelvin; 0 when the disk does not report it.
	if kelvin := binary.LittleEndian.Uint16(log[1:3]); kelvin != 0 {
		disk.Celsius = number(float64(kelvin) - 273)
	}
	return disk, nil
}

// uint128 reads a little-endian 128-bit counter of the NVMe health log.
func uint128(b []byte) float64 {
	return float64(binary.LittleEndian.Uint64(b[:8])) + float64(binary.LittleEndian.Uint64(b[8:16]))*math.Pow(2, 64)
}

func number(f float64) *float64 {
	return &f
}
