package wifi

import (
	"encoding/binary"
	"reflect"
	"testing"
	"unicode/utf16"
)

// interfaceList builds a WLAN_INTERFACE_INFO_LIST as wlanapi.dll returns it.
func interfaceList(interfaces ...wlanInterface) []byte {
	b := make([]byte, interfaceListSize(len(interfaces)))
	binary.LittleEndian.PutUint32(b, uint32(len(interfaces))) //nolint:gosec // a test's few interfaces
	for i, iface := range interfaces {
		info := b[interfaceListHeader+i*interfaceInfoSize:]
		copy(info, iface.guid[:])
		for j, c := range utf16.Encode([]rune(iface.description)) {
			binary.LittleEndian.PutUint16(info[16+2*j:], c)
		}
		if iface.connected {
			binary.LittleEndian.PutUint32(info[16+2*descriptionChars:], wlanInterfaceConnected)
		}
	}
	return b
}

func TestParseInterfacesReadsEachInterface(t *testing.T) {
	want := []wlanInterface{
		{guid: [16]byte{1, 2, 3}, description: "Intel(R) Wi-Fi 6E AX211 160MHz", connected: true},
		{guid: [16]byte{4}, description: "Realtek RTL8821CE 802.11ac PCIe Adapter – zweiter"},
	}
	if got := parseInterfaces(interfaceList(want...)); !reflect.DeepEqual(got, want) {
		t.Errorf("parseInterfaces() = %+v, want %+v", got, want)
	}
}

func TestInterfaceKeyIsTheGUID(t *testing.T) {
	// {00112233-4455-6677-8899-aabbccddeeff}, whose first three parts
	// Windows keeps little-endian.
	iface := wlanInterface{guid: [16]byte{0x33, 0x22, 0x11, 0x00, 0x55, 0x44, 0x77, 0x66, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}}
	if got := iface.key(); got != "00112233445566778899aabbccddeeff" {
		t.Errorf("key() = %q, want the GUID's 32 digits", got)
	}
	if got := idOf(iface.key(), "-quality"); got != "00112233445566778899aabbccddeeff-quality" {
		t.Errorf("idOf(key()) = %q, want the whole GUID", got)
	}
}

func TestParseInterfacesStopsAtTheEnd(t *testing.T) {
	b := interfaceList(wlanInterface{description: "one"}, wlanInterface{description: "two"})
	// Claims three interfaces, but holds two.
	binary.LittleEndian.PutUint32(b, 3)
	if got := parseInterfaces(b); len(got) != 2 {
		t.Errorf("parseInterfaces() = %+v, want the two there are", got)
	}
	if got := parseInterfaces(nil); got != nil {
		t.Errorf("parseInterfaces(nil) = %+v, want nothing", got)
	}
}

func TestSignalQualityReadsTheConnection(t *testing.T) {
	// A WLAN_CONNECTION_ATTRIBUTES as on x64 and arm64: 604 bytes.
	b := make([]byte, 604)
	binary.LittleEndian.PutUint32(b, wlanInterfaceConnected)
	binary.LittleEndian.PutUint32(b[576:], 87)
	if got, ok := signalQuality(b); !ok || got != 87 {
		t.Errorf("signalQuality() = %v, %v, want 87", got, ok)
	}
	if _, ok := signalQuality(b[:579]); ok {
		t.Error("signalQuality() of a short result reports a quality")
	}
}

func TestQualityFromRSSIFollowsWindows(t *testing.T) {
	for dBm, want := range map[float64]float64{-110: 0, -100: 0, -75: 50, -56: 88, -50: 100, -30: 100} {
		if got := qualityFromRSSI(dBm); got != want {
			t.Errorf("qualityFromRSSI(%v) = %v, want %v", dBm, got, want)
		}
	}
}
