package pressure

import "testing"

// Reads the real counters of the machine. NewReader starts the rates a
// second ahead itself, so the first read has every counter.
func TestNewReaderReadsTheCounters(t *testing.T) {
	read := NewReader()
	got := read()
	if len(got) != 1 {
		t.Fatalf("read() = %+v, want the pressure group", got)
	}
	ids := map[string]bool{}
	for _, item := range got[0].Items {
		ids[item.ID] = true
		t.Logf("%s: %v", item.ID, *item.Value)
	}
	for _, id := range []string{"cpu-queue", "memory-hard-faults", "disk-busy"} {
		if !ids[id] {
			t.Errorf("read() lacks %s: %+v", id, got[0].Items)
		}
	}
}
