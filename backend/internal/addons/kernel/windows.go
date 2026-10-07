package kernel

// This file turns what Windows reports into the add-on's counters. It has no
// build constraint so its tests run on every OS; system_windows.go reads it.

import "unsafe"

// tcpStats is a MIB_TCPSTATS of the IP Helper API, as GetTcpStatisticsEx
// fills it for one IP version: fifteen 32-bit numbers.
type tcpStats struct {
	// RtoAlgorithm to EstabResets
	_ [8]uint32
	// currEstab is the connections in the states ESTABLISHED and CLOSE-WAIT.
	currEstab uint32
	// InSegs and OutSegs
	_           [2]uint32
	retransSegs uint32
	// InErrs, OutRsts and NumConns
	_ [3]uint32
}

// tcpCounters adds up the TCP statistics of IPv4 and IPv6: the established
// connections and the retransmitted segments, a running total of which the
// add-on shows the change per second. Linux counts its retransmissions over
// both versions too.
func tcpCounters(versions ...tcpStats) counters {
	if len(versions) == 0 {
		return nil
	}
	found := counters{}
	for _, stats := range versions {
		found[tcpEstablished] += uint64(stats.currEstab)
		found[retransmissions] += uint64(stats.retransSegs)
	}
	return found
}

// performanceInformation is the PERFORMANCE_INFORMATION GetPerformanceInfo
// fills; of it the add-on uses only the handle count.
type performanceInformation struct {
	cb uint32
	// CommitTotal to PageSize
	_           [10]uintptr
	handleCount uint32
	// ProcessCount and ThreadCount
	_ [2]uint32
}

// newPerformanceInformation returns the structure to fill, with its size
// set, as Windows wants it.
func newPerformanceInformation() performanceInformation {
	return performanceInformation{cb: uint32(unsafe.Sizeof(performanceInformation{}))}
}

// handleCounters returns the handles open on the whole machine: files, but
// also registry keys, events, threads and every other kernel object, so the
// value is labelled as handles, not as open files.
func handleCounters(info performanceInformation) counters {
	if info.handleCount == 0 {
		return nil
	}
	return counters{handles: uint64(info.handleCount)}
}
