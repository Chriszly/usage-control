package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

const meminfo = `MemTotal:        8131032 kB
MemFree:          912340 kB
Shmem:             51200 kB
Slab:             204800 kB
SReclaimable:     150000 kB
PageTables:        10240 kB
Dirty:              1024 kB
Writeback:             0 kB
Committed_AS:    4194304 kB
HugePages_Total:       0
`

func writeProc(t *testing.T, dir, meminfoText, vmstatText string) {
	t.Helper()
	for name, text := range map[string]string{"meminfo": meminfoText, "vmstat": vmstatText} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func values(extras []metrics.Extra) map[string]float64 {
	got := map[string]float64{}
	for _, group := range extras {
		for _, item := range group.Items {
			got[item.ID] = *item.Value
		}
	}
	return got
}

func TestReadSizesNowAndRatesFromTheSecondRead(t *testing.T) {
	dir := t.TempDir()
	writeProc(t, dir, meminfo, "pgfault 1000\npgmajfault 10\npswpin 0\npswpout 100\n")
	r := NewReader(dir)
	r.pageSize = 4096
	start := time.Now()

	first := r.Read(start)
	if len(first) != 1 || first[0].ID != "memory" || first[0].Titles["de"] != "Speicherdetails" {
		t.Fatalf("first Read() = %+v, want the memory group", first)
	}
	want := map[string]float64{
		"dirty": 1024 * 1024, "writeback": 0, "slab": 204800 * 1024, "shared": 51200 * 1024,
		"page-tables": 10240 * 1024, "committed": 4194304 * 1024,
	}
	if got := values(first); len(got) != len(want) {
		t.Fatalf("first Read() = %v, want only the sizes %v", got, want)
	}
	for _, item := range first[0].Items {
		if item.Unit != metrics.UnitBytes || !item.History || item.Labels["fr"] == "" || *item.Value != want[item.ID] {
			t.Errorf("item %+v, want %v bytes with history and translations", item, want[item.ID])
		}
	}

	writeProc(t, dir, meminfo, "pgfault 6000\npgmajfault 20\npswpin 10\npswpout 100\n")
	second := r.Read(start.Add(5 * time.Second))

	got := values(second)
	for id, value := range map[string]float64{
		"page-faults": 1000, "major-page-faults": 2, "swap-in": 2 * 4096, "swap-out": 0,
	} {
		if got[id] != value {
			t.Errorf("%s = %v, want %v", id, got[id], value)
		}
	}
	for _, item := range second[0].Items {
		switch item.ID {
		case "swap-in", "swap-out":
			if item.Unit != metrics.UnitBytesPerSecond {
				t.Errorf("%s unit = %s, want bytesPerSecond", item.ID, item.Unit)
			}
		case "page-faults", "major-page-faults":
			if item.Unit != metrics.UnitPerSecond {
				t.Errorf("%s unit = %s, want perSecond", item.ID, item.Unit)
			}
		}
	}
}

func TestReadLeavesOutCountersThatWentBack(t *testing.T) {
	dir := t.TempDir()
	writeProc(t, dir, "", "pgfault 1000\n")
	r := NewReader(dir)
	start := time.Now()
	r.Read(start)
	writeProc(t, dir, "", "pgfault 10\n")

	if got := r.Read(start.Add(5 * time.Second)); got != nil {
		t.Errorf("Read() = %+v, want nothing", got)
	}
}

func TestReadWithoutProcReadsNothing(t *testing.T) {
	r := NewReader(filepath.Join(t.TempDir(), "missing"))
	now := time.Now()
	if got := r.Read(now); got != nil {
		t.Errorf("Read() = %+v, want nil", got)
	}
	if got := r.Read(now.Add(5 * time.Second)); got != nil {
		t.Errorf("second Read() = %+v, want nil", got)
	}
}
