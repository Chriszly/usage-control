package pressure

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestParseReadsAvg10ByLine(t *testing.T) {
	got := parse(`some avg10=12.50 avg60=3.36 avg300=6.24 total=35164045
full avg10=0.00 avg60=3.30 avg300=6.14 total=34639639
broken avg10=abc
odd avg10=250.00
`)
	want := map[string]float64{"some": 12.5, "full": 0}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parse() = %v, want %v", got, want)
	}
}

func TestReadReportsEachValue(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		// Older kernels have no "full" line for the CPU, newer ones report 0.
		"pressure/cpu":    "some avg10=4.20 avg60=1.00 avg300=0.50 total=100\n",
		"pressure/memory": "some avg10=1.50 avg60=0.00 avg300=0.00 total=0\nfull avg10=0.75 avg60=0.00 avg300=0.00 total=0\n",
		"pressure/io":     "some avg10=30.00 avg60=0.00 avg300=0.00 total=0\nfull avg10=20.00 avg60=0.00 avg300=0.00 total=0\n",
	})

	got := Read(dir)

	if len(got) != 1 || got[0].ID != "pressure" || got[0].Titles["de"] == "" {
		t.Fatalf("Read() = %+v, want the pressure group", got)
	}
	want := map[string]float64{"cpu-some": 4.2, "memory-some": 1.5, "memory-full": 0.75, "io-some": 30, "io-full": 20}
	var ids []string
	for _, item := range got[0].Items {
		ids = append(ids, item.ID)
		if item.Value == nil || *item.Value != want[item.ID] || item.Unit != "percent" || !item.History {
			t.Errorf("item %s = %+v, want %v percent with history", item.ID, item, want[item.ID])
		}
		for _, lang := range []string{"de", "fr", "es"} {
			if item.Labels[lang] == "" {
				t.Errorf("item %s has no %s label", item.ID, lang)
			}
		}
	}
	if wantIDs := []string{"cpu-some", "memory-some", "memory-full", "io-some", "io-full"}; !reflect.DeepEqual(ids, wantIDs) {
		t.Errorf("ids = %v, want %v", ids, wantIDs)
	}
}

func TestReadSkipsMissingFiles(t *testing.T) {
	dir := t.TempDir()
	if got := Read(dir); got != nil {
		t.Errorf("Read() without /proc/pressure = %+v, want nothing", got)
	}
	writeFiles(t, dir, map[string]string{"pressure/cpu": "some avg10=1.00 avg60=0.00 avg300=0.00 total=0\n"})
	got := Read(dir)
	if len(got) != 1 || len(got[0].Items) != 1 || got[0].Items[0].ID != "cpu-some" {
		t.Errorf("Read() with only cpu = %+v, want only cpu-some", got)
	}
}
