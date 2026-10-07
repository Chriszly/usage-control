package memory

import (
	"testing"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

func TestCounterItems(t *testing.T) {
	values := map[string]float64{
		`\Memory\Modified Page List Bytes`: 1 << 20,
		`\Memory\Pool Paged Bytes`:         2 << 20,
		`\Memory\Pool Nonpaged Bytes`:      -1, // not a valid reading
		`\Memory\Committed Bytes`:          4 << 30,
		`\Memory\Page Faults/sec`:          1500,
		`\Memory\Page Reads/sec`:           3,
		`\Memory\Pages Input/sec`:          10,
	}

	got := map[string]metrics.ExtraItem{}
	for _, item := range counterItems(values, false, 4096) {
		got[item.ID] = item
	}
	if len(got) != 3 || got["committed"].Label != committed.label || *got["modified"].Value != 1<<20 {
		t.Errorf("counterItems() without rates = %+v, want the three valid sizes", got)
	}
	for id, item := range got {
		if item.Unit != metrics.UnitBytes || !item.History || item.Labels["es"] == "" {
			t.Errorf("%s = %+v, want bytes with history and translations", id, item)
		}
	}

	got = map[string]metrics.ExtraItem{}
	for _, item := range counterItems(values, true, 4096) {
		got[item.ID] = item
	}
	for id, want := range map[string]struct {
		value float64
		unit  metrics.Unit
	}{
		"page-faults": {1500, metrics.UnitPerSecond},
		"page-reads":  {3, metrics.UnitPerSecond},
		"paged-in":    {10 * 4096, metrics.UnitBytesPerSecond},
	} {
		if item, ok := got[id]; !ok || *item.Value != want.value || item.Unit != want.unit {
			t.Errorf("%s = %+v, want %v %s", id, item, want.value, want.unit)
		}
	}
	if _, ok := got["paged-out"]; ok {
		t.Error("paged-out has no value but is shown")
	}
}

func TestCounterIDsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range counters {
		if seen[c.id] || c.label == "" || len(c.labels) != 3 {
			t.Errorf("counter %s: duplicate id or missing labels", c.path)
		}
		seen[c.id] = true
	}
}
