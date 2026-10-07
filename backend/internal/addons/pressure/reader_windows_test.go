package pressure

import (
	"testing"
	"time"
)

// Reads the real counters of the machine: the processor queue right away,
// and every counter from the second reading on.
func TestNewReaderReadsTheCounters(t *testing.T) {
	read := NewReader()
	time.Sleep(time.Second)
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
