package power

import (
	"slices"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/Chriszly/usage-control/backend/internal/pdh"
)

// system reads the power meters Windows offers itself: the "Energy Meter"
// performance counters, which Windows 10 and 11 fill on machines whose
// firmware or CPU has energy meters (Energy Metering Interface, EMI), such as
// the RAPL counters of Intel CPUs and the meters of many laptops, and what
// each battery gives while the PC runs on it.
type system struct {
	query *pdh.Query
	// meters is 0 when the machine has no energy meters.
	meters pdh.Counter
}

func newSystem() *system {
	s := &system{}
	query, err := pdh.Open()
	if err != nil {
		return s
	}
	meters, err := query.Add(`\Energy Meter(*)\Power`)
	if err != nil {
		query.Close()
		return s
	}
	// Power is measured between two readings, so the first one starts it.
	_ = query.Collect()
	s.query, s.meters = query, meters
	return s
}

func (s *system) read() []Reading {
	var readings []Reading
	if s.meters != 0 && s.query.Collect() == nil {
		if values, err := s.meters.Values(); err == nil {
			readings = meterReadings(values)
		}
	}
	return append(readings, readBatteries()...)
}

// guidDeviceBattery is GUID_DEVICE_BATTERY, the device interface of batteries.
var guidDeviceBattery = windows.GUID{Data1: 0x72631e54, Data2: 0x78a4, Data3: 0x11d0, Data4: [8]byte{0xbc, 0xf7, 0x00, 0xaa, 0x00, 0xb7, 0xb3, 0x2a}}

// The battery IOCTLs: CTL_CODE(FILE_DEVICE_BATTERY, 0x10, 0x11 and 0x13,
// METHOD_BUFFERED, FILE_READ_ACCESS).
const (
	ioctlBatteryQueryTag         = 0x294040
	ioctlBatteryQueryInformation = 0x294044
	ioctlBatteryQueryStatus      = 0x29404c

	// batteryCapacityRelative in the capabilities means the battery gives
	// its rate in units of its own, not in milliwatts.
	batteryCapacityRelative = 0x40000000
)

// batteryQueryInformation is BATTERY_QUERY_INFORMATION, asking for
// BatteryInformation (level 0).
type batteryQueryInformation struct {
	tag    uint32
	level  int32
	atRate int32
}

// batteryInformation is BATTERY_INFORMATION.
type batteryInformation struct {
	capabilities        uint32
	technology          uint8
	reserved            [3]uint8
	chemistry           [4]uint8
	designedCapacity    uint32
	fullChargedCapacity uint32
	defaultAlert1       uint32
	defaultAlert2       uint32
	criticalBias        uint32
	cycleCount          uint32
}

// batteryWaitStatus is BATTERY_WAIT_STATUS; all zero but the tag, it asks
// for the status now.
type batteryWaitStatus struct {
	tag, timeout, powerState, lowCapacity, highCapacity uint32
}

// batteryStatus is BATTERY_STATUS.
type batteryStatus struct {
	powerState, capacity, voltage uint32
	rate                          int32
}

// readBatteries returns what each battery gives while the PC runs on it.
func readBatteries() []Reading {
	paths, err := windows.CM_Get_Device_Interface_List("", &guidDeviceBattery, windows.CM_GET_DEVICE_INTERFACE_LIST_PRESENT)
	if err != nil {
		return nil
	}
	slices.Sort(paths)
	var readings []Reading
	for index, path := range paths {
		if reading, ok := readBattery(index, path); ok {
			readings = append(readings, reading)
		}
	}
	return readings
}

// readBattery asks one battery for its state, only reading.
//
//nolint:gosec // DeviceIoControl takes pointers, which need unsafe.
func readBattery(index int, path string) (Reading, bool) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return Reading{}, false
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return Reading{}, false
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	ioctl := func(code uint32, in unsafe.Pointer, inSize uintptr, out unsafe.Pointer, outSize uintptr) bool {
		var returned uint32
		return windows.DeviceIoControl(handle, code, (*byte)(in), uint32(inSize), (*byte)(out), uint32(outSize), &returned, nil) == nil
	}
	var wait, tag uint32
	if !ioctl(ioctlBatteryQueryTag, unsafe.Pointer(&wait), unsafe.Sizeof(wait), unsafe.Pointer(&tag), unsafe.Sizeof(tag)) || tag == 0 {
		return Reading{}, false
	}
	query := batteryQueryInformation{tag: tag}
	var info batteryInformation
	if !ioctl(ioctlBatteryQueryInformation, unsafe.Pointer(&query), unsafe.Sizeof(query), unsafe.Pointer(&info), unsafe.Sizeof(info)) ||
		info.capabilities&batteryCapacityRelative != 0 {
		return Reading{}, false
	}
	request := batteryWaitStatus{tag: tag}
	var status batteryStatus
	if !ioctl(ioctlBatteryQueryStatus, unsafe.Pointer(&request), unsafe.Sizeof(request), unsafe.Pointer(&status), unsafe.Sizeof(status)) {
		return Reading{}, false
	}
	return batteryReading(index, status.powerState, status.rate)
}
