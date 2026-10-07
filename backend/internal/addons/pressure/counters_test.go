package pressure

import (
	"reflect"
	"testing"
)

func float(value float64) *float64 { return &value }

func TestFromCountersReportsEachSignal(t *testing.T) {
	got := fromCounters(counters{processorQueue: float(3), pagesInput: float(120.5), diskIdle: float(82)})

	if len(got) != 1 || got[0].ID != "pressure" || got[0].Titles["de"] == "" {
		t.Fatalf("fromCounters() = %+v, want the pressure group", got)
	}
	want := map[string]struct {
		value float64
		unit  string
	}{
		"cpu-queue":          {3, "number"},
		"memory-hard-faults": {120.5, "perSecond"},
		"disk-busy":          {18, "percent"},
	}
	var ids []string
	for _, item := range got[0].Items {
		ids = append(ids, item.ID)
		w := want[item.ID]
		if item.Value == nil || *item.Value != w.value || string(item.Unit) != w.unit || !item.History {
			t.Errorf("item %s = %+v, want %v %s with history", item.ID, item, w.value, w.unit)
		}
		for _, lang := range []string{"de", "fr", "es"} {
			if item.Labels[lang] == "" {
				t.Errorf("item %s has no %s label", item.ID, lang)
			}
		}
	}
	if wantIDs := []string{"cpu-queue", "memory-hard-faults", "disk-busy"}; !reflect.DeepEqual(ids, wantIDs) {
		t.Errorf("ids = %v, want %v", ids, wantIDs)
	}
}

func TestFromCountersKeepsDiskBusyWithinPercent(t *testing.T) {
	for idle, busy := range map[float64]float64{-3: 100, 0: 100, 100: 0, 104: 0} {
		got := fromCounters(counters{diskIdle: float(idle)})
		if len(got) != 1 || len(got[0].Items) != 1 || *got[0].Items[0].Value != busy {
			t.Errorf("fromCounters(idle %v) = %+v, want disk-busy %v", idle, got, busy)
		}
	}
}

func TestFromCountersSkipsMissingCounters(t *testing.T) {
	if got := fromCounters(counters{}); got != nil {
		t.Errorf("fromCounters() without counters = %+v, want nothing", got)
	}
	got := fromCounters(counters{processorQueue: float(-1), pagesInput: float(0)})
	if len(got) != 1 || len(got[0].Items) != 1 || got[0].Items[0].ID != "memory-hard-faults" {
		t.Errorf("fromCounters() = %+v, want only memory-hard-faults", got)
	}
}

// The Windows values mean something else than the Linux ones, so they must
// never share an id with them.
func TestWindowsIDsDifferFromLinux(t *testing.T) {
	for _, s := range []signal{cpuQueue, memoryHardFaults, diskBusy} {
		for _, v := range values {
			if s.id == v.id {
				t.Errorf("Windows and Linux share the id %s", s.id)
			}
		}
	}
}
