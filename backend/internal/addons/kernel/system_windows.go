package kernel

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/Chriszly/usage-control/backend/internal/pdh"
)

// systemReader reads the closest values Windows itself offers to those of
// /proc:
//
//   - context switches and interrupts per second from the performance
//     counters "\System\Context Switches/sec" and
//     "\Processor Information(_Total)\Interrupts/sec" (package pdh),
//   - the handles open on the machine from GetPerformanceInfo (psapi.dll),
//     shown as handles because they are more than files,
//   - the established TCP connections and the TCP retransmissions of IPv4
//     and IPv6 from GetTcpStatisticsEx (iphlpapi.dll).
//
// Windows counts no new processes and no sockets in use, so those are left
// out. All of these need no privileges.
type systemReader struct {
	meter meter
	tcp   tcpTotals

	// query reads the counters, by id, or is nil when Windows has none.
	query    *pdh.Query
	counters map[string]pdh.Counter
	// started is whether the query has collected once: the counters are
	// rates between two collections.
	started bool
}

var (
	psapi              = windows.NewLazySystemDLL("psapi.dll")
	getPerformanceInfo = psapi.NewProc("GetPerformanceInfo")

	iphlpapi           = windows.NewLazySystemDLL("iphlpapi.dll")
	getTCPStatisticsEx = iphlpapi.NewProc("GetTcpStatisticsEx")
)

// NewSystemReader returns the reader of this machine. A counter that cannot
// be opened is left out.
func NewSystemReader() Source {
	r := &systemReader{counters: map[string]pdh.Counter{}}
	query, err := pdh.Open()
	if err != nil {
		return r
	}
	r.query = query
	for id, path := range map[string]string{
		contextSwitches: `\System\Context Switches/sec`,
		interrupts:      `\Processor Information(_Total)\Interrupts/sec`,
	} {
		if counter, err := query.Add(path); err == nil {
			r.counters[id] = counter
		}
	}
	return r
}

// Read returns the values by id. Rates are the average since the previous
// call, so the first call leaves them out.
func (r *systemReader) Read(now time.Time) map[string]float64 {
	values := r.meter.measure(now, handleCounters(performanceInfo()), r.tcp.counters(tcpStatistics()))
	if r.query == nil || len(r.counters) == 0 {
		return values
	}
	if err := r.query.Collect(); err != nil {
		r.started = false
		return values
	}
	if !r.started {
		r.started = true
		return values
	}
	for id, counter := range r.counters {
		// Each counter names one instance, so it has one value.
		found, err := counter.Values()
		if err != nil || len(found) != 1 {
			continue
		}
		for _, value := range found {
			if value >= 0 {
				values[id] = value
			}
		}
	}
	return values
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
// has, by version.
//
//nolint:gosec // iphlpapi.dll takes a pointer, which needs unsafe.
func tcpStatistics() map[string]tcpStats {
	found := map[string]tcpStats{}
	for version, family := range map[string]uintptr{"IPv4": windows.AF_INET, "IPv6": windows.AF_INET6} {
		var stats tcpStats
		if ret, _, _ := getTCPStatisticsEx.Call(uintptr(unsafe.Pointer(&stats)), family); ret == 0 {
			found[version] = stats
		}
	}
	return found
}
