package wifi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
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
// and again when it changes, not every few seconds. While nothing is
// connected, the problem logged last stands: it shows again at the next
// connection, so a disconnect is not reported as reading in full again.
//
// It keeps one WLAN handle open, opened at the first read and again after
// the API failed, and closes it when ctx of the first read is done.
func NewReader() func(ctx context.Context) []Reading {
	var (
		mu       sync.Mutex
		watching sync.Once
		handle   windows.Handle
		open     bool
		closed   bool
		logged   string
	)
	closeHandle := func() {
		if open {
			_, _, _ = wlanCloseHandle.Call(uintptr(handle), 0)
			open = false
		}
	}
	return func(ctx context.Context) []Reading {
		watching.Do(func() {
			context.AfterFunc(ctx, func() {
				mu.Lock()
				defer mu.Unlock()
				closeHandle()
				closed = true
			})
		})
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return nil
		}
		var readings []Reading
		var connected int
		problem := ""
		if !open {
			handle, problem = openWLAN()
			open = problem == ""
		}
		if open {
			var ok bool
			readings, connected, problem, ok = readWLAN(handle)
			if !ok {
				// The handle may have gone with the WLAN AutoConfig
				// service; the next read opens a new one.
				closeHandle()
			}
		}
		if problem = standingProblem(logged, problem, connected); problem != logged {
			if problem != "" {
				slog.Info("Wi-Fi is read only in part or not at all", "reason", problem)
			} else {
				slog.Info("Wi-Fi is read in full again")
			}
			logged = problem
		}
		return readings
	}
}

// openWLAN opens a WLAN handle, or says why it cannot.
//
//nolint:gosec // wlanapi.dll takes and returns pointers, which need unsafe.
func openWLAN() (windows.Handle, string) {
	if err := wlanapi.Load(); err != nil {
		return 0, "Windows has no Native Wifi API (wlanapi.dll) here"
	}
	var version uint32
	var handle windows.Handle
	ret, _, _ := wlanOpenHandle.Call(wlanClientVersion, 0, uintptr(unsafe.Pointer(&version)), uintptr(unsafe.Pointer(&handle)))
	if ret != 0 {
		if errors.Is(windows.Errno(ret), windows.ERROR_SERVICE_NOT_ACTIVE) {
			return 0, "the WLAN AutoConfig service is not running"
		}
		return 0, fmt.Sprintf("WlanOpenHandle: %v", windows.Errno(ret))
	}
	return handle, ""
}

// readWLAN reads each connected interface through handle, tells how many
// are connected, and says why when it can read less than all of them. It
// reports false when the interfaces could not be listed at all.
//
//nolint:gosec // wlanapi.dll takes and returns pointers, which need unsafe.
func readWLAN(handle windows.Handle) ([]Reading, int, string, bool) {
	var list *byte
	if ret, _, _ := wlanEnumInterfaces.Call(uintptr(handle), 0, uintptr(unsafe.Pointer(&list))); ret != 0 {
		return nil, 0, fmt.Sprintf("WlanEnumInterfaces: %v", windows.Errno(ret)), false
	}
	count := *(*uint32)(unsafe.Pointer(list))
	interfaces := parseInterfaces(unsafe.Slice(list, interfaceListSize(int(count))))
	_, _, _ = wlanFreeMemory.Call(uintptr(unsafe.Pointer(list)))

	var readings []Reading
	connected := 0
	problem := ""
	for _, iface := range interfaces {
		if !iface.connected {
			continue
		}
		connected++
		reading := Reading{Interface: iface.description, Key: iface.key()}
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
	return readings, connected, problem, true
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
