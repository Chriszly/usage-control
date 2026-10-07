package memory

import (
	"testing"
	"time"
)

// TestMemoryCountersOnWindows reads the counters of the machine the test
// runs on: every one exists on every Windows, the sizes at once and the rates
// from the second read on.
func TestMemoryCountersOnWindows(t *testing.T) {
	r, err := openCounters()
	if err != nil {
		t.Fatal(err)
	}
	if len(r.counters) != len(counters) {
		t.Errorf("opened %d of %d counters", len(r.counters), len(counters))
	}
	first := values(r.Read(time.Now()))
	for _, id := range []string{"modified", "pool-paged", "pool-nonpaged", "committed"} {
		if _, ok := first[id]; !ok {
			t.Errorf("first read lacks %s: %v", id, first)
		}
	}
	if _, ok := first["page-faults"]; ok {
		t.Errorf("first read has a rate: %v", first)
	}
	if first["committed"] <= 0 {
		t.Errorf("committed = %v, want more than 0", first["committed"])
	}
	time.Sleep(time.Second)
	second := values(r.Read(time.Now()))
	for _, c := range counters {
		if _, ok := second[c.id]; !ok {
			t.Errorf("second read lacks %s: %v", c.id, second)
		}
	}
}
