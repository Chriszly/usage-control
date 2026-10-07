package metrics

import (
	"math"
	stdnet "net"

	"golang.org/x/sys/windows"
)

// hardwareInterface is the HardwareInterface bit of MIB_IF_ROW2's
// InterfaceAndOperStatusFlags: the adapter is a device of its own, not one
// that Hyper-V, a VPN or another program made up.
const hardwareInterface = 1 << 0

// isVirtualAdapter reports whether Windows marks an adapter as not hardware.
// An adapter it cannot ask about is not left out.
func isVirtualAdapter(iface stdnet.Interface) bool {
	if iface.Index <= 0 || iface.Index > math.MaxUint32 {
		return false
	}
	row := windows.MibIfRow2{InterfaceIndex: uint32(iface.Index)}
	if windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &row) != nil {
		return false
	}
	return !isHardware(row.InterfaceAndOperStatusFlags)
}

// isHardware reports whether InterfaceAndOperStatusFlags mark a hardware
// adapter.
func isHardware(flags uint8) bool {
	return flags&hardwareInterface != 0
}
