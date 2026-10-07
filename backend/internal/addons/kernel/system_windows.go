package kernel

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// systemReader reads the closest values Windows itself offers to those of
// /proc:
//
//   - context switches and interrupts per second from the performance
//     counters "\System\Context Switches/sec" and
//     "\Processor Information(_Total)\Interrupts/sec" (pdh.dll),
//   - the handles open on the machine from GetPerformanceInfo (psapi.dll),
//     shown as handles because they are more than files,
//   - the established TCP connections and the TCP retransmissions of IPv4
//     and IPv6 from GetTcpStatisticsEx (iphlpapi.dll).
//
// Windows counts no new processes and no sockets in use, so those are left
// out. All of these need no privileges.
type systemReader struct {
	meter meter

	query           uintptr
	contextSwitches uintptr
	interrupts      uintptr
	// started is whether the query has collected once: the counters are
	// rates between two collections.
	started bool
}

var (
	pdh                         = windows.NewLazySystemDLL("pdh.dll")
	pdhOpenQuery                = pdh.NewProc("PdhOpenQueryW")
	pdhAddEnglishCounter        = pdh.NewProc("PdhAddEnglishCounterW")
	pdhCollectQueryData         = pdh.NewProc("PdhCollectQueryData")
	pdhGetFormattedCounterValue = pdh.NewProc("PdhGetFormattedCounterValue")

	psapi              = windows.NewLazySystemDLL("psapi.dll")
	getPerformanceInfo = psapi.NewProc("GetPerformanceInfo")

	iphlpapi           = windows.NewLazySystemDLL("iphlpapi.dll")
	getTCPStatisticsEx = iphlpapi.NewProc("GetTcpStatisticsEx")
)

const pdhFormatDouble = 0x00000200

// pdhCounterValue is a PDH_FMT_COUNTERVALUE holding a double.
type pdhCounterValue struct {
	status uint32
	_      uint32
	value  float64
}

// NewSystemReader returns the reader of this machine. A counter that cannot
// be opened is left out.
//
//nolint:gosec // pdh.dll takes pointers, which need unsafe.
func NewSystemReader() Source {
	r := &systemReader{}
	if ret, _, _ := pdhOpenQuery.Call(0, 0, uintptr(unsafe.Pointer(&r.query))); ret != 0 {
		r.query = 0
		return r
	}
	add := func(path string, counter *uintptr) {
		if ret, _, _ := pdhAddEnglishCounter.Call(r.query, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(path))), 0, uintptr(unsafe.Pointer(counter))); ret != 0 {
			*counter = 0
		}
	}
	add(`\System\Context Switches/sec`, &r.contextSwitches)
	add(`\Processor Information(_Total)\Interrupts/sec`, &r.interrupts)
	return r
}

// Read returns the values by id. Rates are the average since the previous
// call, so the first call leaves them out.
func (r *systemReader) Read(now time.Time) map[string]float64 {
	values := r.meter.measure(now, handleCounters(performanceInfo()), tcpCounters(tcpStatistics()...))
	if r.query == 0 {
		return values
	}
	if ret, _, _ := pdhCollectQueryData.Call(r.query); ret != 0 {
		r.started = false
		return values
	}
	if !r.started {
		r.started = true
		return values
	}
	for id, counter := range map[string]uintptr{contextSwitches: r.contextSwitches, interrupts: r.interrupts} {
		if value, ok := counterValue(counter); ok {
			values[id] = value
		}
	}
	return values
}

// counterValue returns the value of a counter of the query since its
// previous collection.
//
//nolint:gosec // pdh.dll takes pointers, which need unsafe.
func counterValue(counter uintptr) (float64, bool) {
	if counter == 0 {
		return 0, false
	}
	var value pdhCounterValue
	ret, _, _ := pdhGetFormattedCounterValue.Call(counter, pdhFormatDouble, 0, uintptr(unsafe.Pointer(&value)))
	// PDH_CSTATUS_VALID_DATA and PDH_CSTATUS_NEW_DATA
	if ret != 0 || (value.status != 0 && value.status != 1) || value.value < 0 {
		return 0, false
	}
	return value.value, true
}

// performanceInfo returns the system's performance information, or nothing
// when Windows does not give it.
//
//nolint:gosec // psapi.dll takes a pointer, which needs unsafe.
func performanceInfo() performanceInformation {
	info := newPerformanceInformation()
	if ok, _, _ := getPerformanceInfo.Call(uintptr(unsafe.Pointer(&info)), uintptr(info.cb)); ok == 0 {
		return performanceInformation{}
	}
	return info
}

// tcpStatistics returns the TCP statistics of each IP version the machine
// has.
//
//nolint:gosec // iphlpapi.dll takes a pointer, which needs unsafe.
func tcpStatistics() []tcpStats {
	var found []tcpStats
	for _, family := range []uintptr{windows.AF_INET, windows.AF_INET6} {
		var stats tcpStats
		if ret, _, _ := getTCPStatisticsEx.Call(uintptr(unsafe.Pointer(&stats)), family); ret == 0 {
			found = append(found, stats)
		}
	}
	return found
}
