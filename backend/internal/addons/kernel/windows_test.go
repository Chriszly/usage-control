package kernel

import (
	"reflect"
	"testing"
	"time"
	"unsafe"
)

func TestWindowsStructsHaveTheSizesWindowsExpects(t *testing.T) {
	if got := unsafe.Sizeof(tcpStats{}); got != 15*4 {
		t.Errorf("MIB_TCPSTATS is %d bytes, want 60", got)
	}
	// cb, ten SIZE_Ts and three DWORDs, aligned to the SIZE_T: 104 bytes
	// on 64-bit Windows, 56 on 32-bit.
	want := uintptr(56)
	if unsafe.Sizeof(uintptr(0)) == 8 {
		want = 104
	}
	if got := newPerformanceInformation().cb; uintptr(got) != want {
		t.Errorf("PERFORMANCE_INFORMATION is %d bytes, want %d", got, want)
	}
}

func TestTCPCountersAddUpIPv4AndIPv6(t *testing.T) {
	var totals tcpTotals
	first := totals.counters(map[string]tcpStats{
		"IPv4": {currEstab: 30, retransSegs: 10},
		"IPv6": {currEstab: 4, retransSegs: 2},
	})
	// The running total of retransmissions starts at the first read.
	if want := (counters{tcpEstablished: 34, retransmissions: 0}); !reflect.DeepEqual(first, want) {
		t.Errorf("first counters() = %v, want %v", first, want)
	}

	got := totals.counters(map[string]tcpStats{
		"IPv4": {currEstab: 31, retransSegs: 15},
		"IPv6": {currEstab: 5, retransSegs: 4},
	})

	if want := (counters{tcpEstablished: 36, retransmissions: 7}); !reflect.DeepEqual(got, want) {
		t.Errorf("second counters() = %v, want %v", got, want)
	}
	if got := totals.counters(nil); got != nil {
		t.Errorf("counters() without statistics = %v, want nil", got)
	}
}

func TestTCPConnectionsLeaveOutAReadWithAVersionMissing(t *testing.T) {
	var totals tcpTotals
	totals.counters(map[string]tcpStats{"IPv4": {currEstab: 30}, "IPv6": {currEstab: 4}})

	got := totals.counters(map[string]tcpStats{"IPv4": {currEstab: 31}})
	if _, ok := got[tcpEstablished]; ok {
		t.Errorf("counters() = %v with IPv6 missing, want no established connections rather than a dip", got)
	}
	// From then on IPv4 alone is all there is.
	got = totals.counters(map[string]tcpStats{"IPv4": {currEstab: 32}})
	if got[tcpEstablished] != 32 {
		t.Errorf("counters() = %v, want 32 established connections", got)
	}
}

func TestTCPRetransmissionsSkipAVersionMissingOnEitherRead(t *testing.T) {
	var totals tcpTotals
	var m meter
	start := time.Now()
	read := func(seconds int, versions map[string]tcpStats) map[string]float64 {
		return m.measure(start.Add(time.Duration(seconds)*time.Second), totals.counters(versions))
	}
	read(0, map[string]tcpStats{"IPv4": {retransSegs: 100}, "IPv6": {retransSegs: 5000}})

	// IPv6 fails once: only IPv4's change counts, now and when IPv6 is back,
	// instead of all of IPv6's 5000 at once.
	steps := []struct {
		versions map[string]tcpStats
		want     float64
	}{
		{map[string]tcpStats{"IPv4": {retransSegs: 110}}, 2},
		{map[string]tcpStats{"IPv4": {retransSegs: 120}, "IPv6": {retransSegs: 5010}}, 2},
		{map[string]tcpStats{"IPv4": {retransSegs: 130}, "IPv6": {retransSegs: 5020}}, 4},
	}
	for i, step := range steps {
		if got := read(5*(i+1), step.versions); got[retransmissions] != step.want {
			t.Errorf("read %d: %v retransmissions per second, want %v", i+2, got[retransmissions], step.want)
		}
	}
}

func TestTCPRetransmissionsCountAcrossTheWrap(t *testing.T) {
	var totals tcpTotals
	totals.counters(map[string]tcpStats{"IPv4": {retransSegs: 1<<32 - 6}})

	got := totals.counters(map[string]tcpStats{"IPv4": {retransSegs: 4}})

	if got[retransmissions] != 10 {
		t.Errorf("counters() = %v, want 10 retransmissions across the wrap", got)
	}
}

func TestHandleCounters(t *testing.T) {
	if got := handleCounters(performanceInformation{handleCount: 81234}); !reflect.DeepEqual(got, counters{handles: 81234}) {
		t.Errorf("handleCounters() = %v, want 81234 handles", got)
	}
	if got := handleCounters(performanceInformation{}); got != nil {
		t.Errorf("handleCounters() without information = %v, want nil", got)
	}
}

func TestWindowsCountersMeasureRetransmissionsPerSecond(t *testing.T) {
	var m meter
	var totals tcpTotals
	start := time.Now()
	first := m.measure(start, handleCounters(performanceInformation{handleCount: 900}), totals.counters(map[string]tcpStats{"IPv4": {currEstab: 5, retransSegs: 100}}))
	if want := map[string]float64{handles: 900, tcpEstablished: 5}; !reflect.DeepEqual(first, want) {
		t.Fatalf("first measure() = %v, want %v", first, want)
	}

	got := m.measure(start.Add(5*time.Second), totals.counters(map[string]tcpStats{"IPv4": {currEstab: 6, retransSegs: 110}}))

	if want := map[string]float64{tcpEstablished: 6, retransmissions: 2}; !reflect.DeepEqual(got, want) {
		t.Errorf("second measure() = %v, want %v", got, want)
	}
}

func TestWindowsValuesHaveLabels(t *testing.T) {
	got := Extras(map[string]float64{handles: 1, tcpEstablished: 2})
	for _, item := range got[0].Items {
		if item.Label == "" || item.Labels["de"] == "" || item.Labels["es"] == "" || item.Unit != "number" {
			t.Errorf("item %+v wants a label, translations and the unit number", item)
		}
	}
}
