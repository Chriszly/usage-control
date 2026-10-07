package wifi

import (
	"encoding/binary"
	"fmt"
	"unicode/utf16"
)

// What Windows' Native Wifi API (wlanapi.dll) returns, read from its memory
// as bytes so it can be tested on any system; reader_windows.go calls it.

const (
	// wlanInterfaceConnected is wlan_interface_state_connected.
	wlanInterfaceConnected = 1

	// A WLAN_INTERFACE_INFO_LIST is two DWORDs, the number of interfaces and
	// an index, followed by a WLAN_INTERFACE_INFO per interface: its GUID,
	// its description as 256 WCHARs and its WLAN_INTERFACE_STATE.
	interfaceListHeader = 8
	descriptionChars    = 256
	interfaceInfoSize   = 16 + 2*descriptionChars + 4

	// signalQualityOffset is where WLAN_CONNECTION_ATTRIBUTES keeps the
	// signal quality (0 to 100): after the state, the connection mode and
	// the profile name (256 WCHARs), in WLAN_ASSOCIATION_ATTRIBUTES after
	// the SSID (36 bytes), the BSS type, the BSSID (6 bytes, padded to 8),
	// the PHY type and the PHY index.
	signalQualityOffset = 4 + 4 + 2*descriptionChars + 36 + 4 + 8 + 4 + 4
)

// wlanInterface is one interface of a WLAN_INTERFACE_INFO_LIST.
type wlanInterface struct {
	guid        [16]byte
	description string
	connected   bool
}

// key returns the interface's GUID as 32 hex digits, in the order of its
// usual form ({00112233-4455-6677-8899-aabbccddeeff}): the GUID tells two
// adapters of the same model apart, which share a description, and stays the
// same across reboots.
func (w wlanInterface) key() string {
	g := w.guid[:]
	return fmt.Sprintf("%08x%04x%04x%x", binary.LittleEndian.Uint32(g), binary.LittleEndian.Uint16(g[4:]),
		binary.LittleEndian.Uint16(g[6:]), g[8:])
}

// interfaceListSize returns how many bytes a WLAN_INTERFACE_INFO_LIST of
// count interfaces takes.
func interfaceListSize(count int) int {
	return interfaceListHeader + count*interfaceInfoSize
}

// parseInterfaces reads a WLAN_INTERFACE_INFO_LIST, leaving out what lies
// beyond b.
func parseInterfaces(b []byte) []wlanInterface {
	if len(b) < interfaceListHeader {
		return nil
	}
	count := binary.LittleEndian.Uint32(b)
	var interfaces []wlanInterface
	for i := range int(count) {
		start := interfaceListHeader + i*interfaceInfoSize
		if start+interfaceInfoSize > len(b) {
			break
		}
		info := b[start : start+interfaceInfoSize]
		var iface wlanInterface
		copy(iface.guid[:], info[:16])
		iface.description = utf16String(info[16 : 16+2*descriptionChars])
		iface.connected = binary.LittleEndian.Uint32(info[16+2*descriptionChars:]) == wlanInterfaceConnected
		interfaces = append(interfaces, iface)
	}
	return interfaces
}

// signalQuality reads the signal quality from a WLAN_CONNECTION_ATTRIBUTES,
// or reports false when b is too short to hold it.
func signalQuality(b []byte) (float64, bool) {
	if len(b) < signalQualityOffset+4 {
		return 0, false
	}
	return min(100, float64(binary.LittleEndian.Uint32(b[signalQualityOffset:]))), true
}

// qualityFromRSSI turns a signal in dBm into Windows' signal quality, which
// its documentation defines as 0 at -100 dBm and 100 at -50 dBm, linear in
// between.
func qualityFromRSSI(dBm float64) float64 {
	return min(100, max(0, (dBm+100)*2))
}

// standingProblem returns the problem a read leaves logged: the one it
// found, or, when it found none but nothing is connected, the one logged
// before if that came from reading a connection (ofConnection), as with
// location access denied, which a read without connections cannot run into.
// So a disconnect is not taken for that problem gone, nor the next
// connection for it come back. A problem with the API itself, such as the
// WLAN AutoConfig service not running, is gone once a read finds none.
func standingProblem(logged string, ofConnection bool, found string, connected int) string {
	if found == "" && connected == 0 && ofConnection {
		return logged
	}
	return found
}

// utf16String reads a NUL-terminated little-endian UTF-16 string.
func utf16String(b []byte) string {
	chars := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		c := binary.LittleEndian.Uint16(b[i:])
		if c == 0 {
			break
		}
		chars = append(chars, c)
	}
	return string(utf16.Decode(chars))
}
