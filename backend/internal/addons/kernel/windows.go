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

// tcpTotals adds up the TCP statistics of IPv4 and IPv6, as Linux counts
// over both versions too: the established connections and the retransmitted
// segments, a running total of which the add-on shows the change per second.
//
// Windows keeps a running total of retransmissions for each version, and a
// read can lack a version for which GetTcpStatisticsEx failed. Adding up the
// totals would then jump by a whole version's total when it answers again,
// so the running total grows instead by each version's change since the
// previous read, and a version missing on either read adds nothing.
type tcpTotals struct {
	// previous is each version's retransmissions at the previous read.
	previous map[string]uint32
	// retransmitted is the running total the add-on shows the change of.
	retransmitted uint64
}

// counters turns the TCP statistics of the versions that answered on this
// read, by version, into the add-on's counters.
func (t *tcpTotals) counters(versions map[string]tcpStats) counters {
	previous := t.previous
	t.previous = map[string]uint32{}
	if len(versions) == 0 {
		return nil
	}
	found := counters{}
	for version, stats := range versions {
		found[tcpEstablished] += uint64(stats.currEstab)
		if before, ok := previous[version]; ok {
			// A 32-bit total, which wraps; subtracting in 32 bits gives the
			// change across the wrap too. It only starts again with Windows,
			// and so does the add-on.
			t.retransmitted += uint64(stats.retransSegs - before)
		}
		t.previous[version] = stats.retransSegs
	}
	// A version that answered last time but not now would make the
	// connections dip for one reading, so they are left out of this one.
	if len(versions) < len(previous) {
		delete(found, tcpEstablished)
	}
	found[retransmissions] = t.retransmitted
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
