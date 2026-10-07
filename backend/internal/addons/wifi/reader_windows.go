package wifi

import (
	"errors"
	"fmt"
	"log/slog"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// wlanClientVersion 2 is the API of Windows Vista and later.
	wlanClientVersion = 2
	// opCurrentConnection is wlan_intf_opcode_current_connection, which
	// returns a WLAN_CONNECTION_ATTRIBUTES.
	opCurrentConnection = 7
	// opRSSI is wlan_intf_opcode_rssi, which returns a LONG in dBm.
	opRSSI = 0x10000102
)

var (
	wlanapi            = windows.NewLazySystemDLL("wlanapi.dll")
	wlanOpenHandle     = wlanapi.NewProc("WlanOpenHandle")
	wlanCloseHandle    = wlanapi.NewProc("WlanCloseHandle")
	wlanEnumInterfaces = wlanapi.NewProc("WlanEnumInterfaces")
	wlanQueryInterface = wlanapi.NewProc("WlanQueryInterface")
	wlanFreeMemory     = wlanapi.NewProc("WlanFreeMemory")
)

// NewReader returns what reads each connected wireless interface, through
// Windows' Native Wifi API in wlanapi.dll. Without the API (Windows Server
// without its Wireless LAN feature), with the WLAN AutoConfig service
// stopped or without a Wi-Fi adapter it reads nothing; why is logged once,
// and again when it changes, not every few seconds.
func NewReader() func() []Reading {
	logged := ""
	return func() []Reading {
		readings, problem := readWLAN()
		if problem != logged {
			if problem != "" {
				slog.Info("Wi-Fi is read only in part or not at all", "reason", problem)
			} else if logged != "" {
				slog.Info("Wi-Fi is read in full again")
			}
			logged = problem
		}
		return readings
	}
}

// readWLAN reads each connected interface, and says why when it can read
// less than all of it.
//
//nolint:gosec // wlanapi.dll takes and returns pointers, which need unsafe.
func readWLAN() ([]Reading, string) {
	if err := wlanapi.Load(); err != nil {
		return nil, "Windows has no Native Wifi API (wlanapi.dll) here"
	}
	var version uint32
	var handle windows.Handle
	ret, _, _ := wlanOpenHandle.Call(wlanClientVersion, 0, uintptr(unsafe.Pointer(&version)), uintptr(unsafe.Pointer(&handle)))
	if ret != 0 {
		if errors.Is(windows.Errno(ret), windows.ERROR_SERVICE_NOT_ACTIVE) {
			return nil, "the WLAN AutoConfig service is not running"
		}
		return nil, fmt.Sprintf("WlanOpenHandle: %v", windows.Errno(ret))
	}
	defer wlanCloseHandle.Call(uintptr(handle), 0) //nolint:errcheck // nothing to do when closing fails

	var list *byte
	if ret, _, _ := wlanEnumInterfaces.Call(uintptr(handle), 0, uintptr(unsafe.Pointer(&list))); ret != 0 {
		return nil, fmt.Sprintf("WlanEnumInterfaces: %v", windows.Errno(ret))
	}
	count := *(*uint32)(unsafe.Pointer(list))
	interfaces := parseInterfaces(unsafe.Slice(list, interfaceListSize(int(count))))
	_, _, _ = wlanFreeMemory.Call(uintptr(unsafe.Pointer(list)))

	var readings []Reading
	problem := ""
	for _, iface := range interfaces {
		if !iface.connected {
			continue
		}
		reading := Reading{Interface: iface.description}
		if dBm, err := queryRSSI(handle, &iface.guid); err == nil && dBm < 0 {
			reading.SignalDBm = float64(dBm)
			reading.HasSignal = true
		}
		quality, err := queryQuality(handle, &iface.guid)
		switch {
		case err == nil:
			reading.QualityPercent = quality
		case reading.HasSignal:
			// Windows 11 24H2 and later deny the current connection to
			// programs without location access, as it holds the network's
			// name; the quality then follows from the signal, by Windows'
			// own rule.
			reading.QualityPercent = qualityFromRSSI(reading.SignalDBm)
			problem = fmt.Sprintf("the quality follows from the signal, since the current connection is not readable: %v", err)
		default:
			problem = fmt.Sprintf("%s: neither the connection nor the signal is readable: %v", iface.description, err)
			continue
		}
		readings = append(readings, reading)
	}
	return readings, problem
}

// queryQuality returns the signal quality of the interface's current
// connection.
//
//nolint:gosec // wlanapi.dll takes and returns pointers, which need unsafe.
func queryQuality(handle windows.Handle, guid *[16]byte) (float64, error) {
	data, size, err := query(handle, guid, opCurrentConnection)
	if err != nil {
		return 0, err
	}
	defer wlanFreeMemory.Call(uintptr(unsafe.Pointer(data))) //nolint:errcheck // WlanFreeMemory returns nothing
	quality, ok := signalQuality(unsafe.Slice(data, size))
	if !ok {
		return 0, fmt.Errorf("the current connection takes %d bytes, too few", size)
	}
	return quality, nil
}

// queryRSSI returns the interface's signal in dBm.
//
//nolint:gosec // wlanapi.dll takes and returns pointers, which need unsafe.
func queryRSSI(handle windows.Handle, guid *[16]byte) (int32, error) {
	data, size, err := query(handle, guid, opRSSI)
	if err != nil {
		return 0, err
	}
	defer wlanFreeMemory.Call(uintptr(unsafe.Pointer(data))) //nolint:errcheck // WlanFreeMemory returns nothing
	if size < 4 {
		return 0, fmt.Errorf("the signal takes %d bytes, too few", size)
	}
	return *(*int32)(unsafe.Pointer(data)), nil
}

// query calls WlanQueryInterface, whose result the caller frees with
// WlanFreeMemory.
//
//nolint:gosec // wlanapi.dll takes and returns pointers, which need unsafe.
func query(handle windows.Handle, guid *[16]byte, opCode uint32) (*byte, uint32, error) {
	var size uint32
	var data *byte
	ret, _, _ := wlanQueryInterface.Call(uintptr(handle), uintptr(unsafe.Pointer(guid)), uintptr(opCode), 0,
		uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&data)), 0)
	if ret != 0 {
		return nil, 0, windows.Errno(ret)
	}
	if data == nil {
		return nil, 0, errors.New("no data")
	}
	return data, size, nil
}
