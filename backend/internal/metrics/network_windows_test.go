package metrics

import "testing"

func TestIsHardwareReadsTheHardwareInterfaceBit(t *testing.T) {
	// HardwareInterface is the lowest bit; ConnectorPresent (0x04) alone,
	// as on some virtual adapters, is not hardware.
	for flags, want := range map[uint8]bool{0x00: false, 0x01: true, 0x05: true, 0x04: false} {
		if got := isHardware(flags); got != want {
			t.Errorf("isHardware(%#x) = %v, want %v", flags, got, want)
		}
	}
}
