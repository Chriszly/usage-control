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
	v4 := tcpStats{currEstab: 30, retransSegs: 10}
	v6 := tcpStats{currEstab: 4, retransSegs: 2}

	got := tcpCounters(v4, v6)

	if want := (counters{tcpEstablished: 34, retransmissions: 12}); !reflect.DeepEqual(got, want) {
		t.Errorf("tcpCounters() = %v, want %v", got, want)
	}
	if got := tcpCounters(); got != nil {
		t.Errorf("tcpCounters() without statistics = %v, want nil", got)
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
	start := time.Now()
	first := m.measure(start, handleCounters(performanceInformation{handleCount: 900}), tcpCounters(tcpStats{currEstab: 5, retransSegs: 100}))
	if want := map[string]float64{handles: 900, tcpEstablished: 5}; !reflect.DeepEqual(first, want) {
		t.Fatalf("first measure() = %v, want %v", first, want)
	}

	got := m.measure(start.Add(5*time.Second), tcpCounters(tcpStats{currEstab: 6, retransSegs: 110}))

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
