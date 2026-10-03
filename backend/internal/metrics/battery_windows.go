package metrics

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var getSystemPowerStatus = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetSystemPowerStatus")

// systemPowerStatus is SYSTEM_POWER_STATUS in the Windows API.
type systemPowerStatus struct {
	acLineStatus        byte
	batteryFlag         byte
	batteryLifePercent  byte
	systemStatusFlag    byte
	batteryLifeTime     uint32
	batteryFullLifeTime uint32
}

const (
	noSystemBattery = 128 // in batteryFlag
	unknownStatus   = 255 // in batteryFlag and batteryLifePercent
	acOnline        = 1   // in acLineStatus
)

// batteryReader asks Windows for the charge of all batteries together, in
// one call.
type batteryReader struct{}

func newBatteryReader() *batteryReader { return &batteryReader{} }

// read returns the charge, or nil when the PC has no battery.
//
//nolint:gosec // GetSystemPowerStatus takes a pointer, which needs unsafe.
func (*batteryReader) read() *Battery {
	var status systemPowerStatus
	if ret, _, _ := getSystemPowerStatus.Call(uintptr(unsafe.Pointer(&status))); ret == 0 {
		return nil
	}
	if status.batteryFlag&noSystemBattery != 0 || status.batteryFlag == unknownStatus || status.batteryLifePercent == unknownStatus {
		return nil
	}
	return &Battery{
		Percent:   float64(min(status.batteryLifePercent, 100)),
		PluggedIn: status.acLineStatus == acOnline,
	}
}
