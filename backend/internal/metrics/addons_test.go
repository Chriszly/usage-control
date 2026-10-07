package metrics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAddOnsReadsTheFreshReports(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	write := func(name string, at time.Time, id string) {
		t.Helper()
		report := AddOnReport{Time: at, Extras: []Extra{{ID: id, Title: id, Items: []ExtraItem{{ID: "a", Unit: UnitWatts, Value: number(1)}}}}}
		if err := WriteAddOnReport(filepath.Join(dir, name), report); err != nil {
			t.Fatal(err)
		}
	}
	write("b-power.json", now, "power")
	write("a-old.json", now.Add(-time.Minute), "old")
	write("c-pressure.json", now.Add(-5*time.Second), "pressure")
	write("f-future.json", now.Add(time.Hour), "future")
	if err := os.WriteFile(filepath.Join(dir, "d-broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "e-huge.json"), []byte(strings.Repeat(" ", maxAddOnFileBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}

	got := (&AddOns{Dir: dir, MaxEntries: 64}).Read(now)

	if len(got) != 2 || got[0].ID != "power" || got[1].ID != "pressure" {
		t.Errorf("Read() = %+v, want only the power and pressure reports, in file order", got)
	}
}

func TestAddOnsReadAFileAgainOnlyOnceItChanged(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "power.json")
	now := time.Now().UTC().Truncate(time.Second)
	write := func(id string) {
		t.Helper()
		report := AddOnReport{Time: now, Extras: []Extra{{ID: id, Title: id, Items: []ExtraItem{{ID: "a", Unit: UnitWatts, Value: number(1)}}}}}
		if err := WriteAddOnReport(file, report); err != nil {
			t.Fatal(err)
		}
	}
	addOns := &AddOns{Dir: dir, MaxEntries: 64}
	write("first")
	if got := addOns.Read(now); len(got) != 1 || got[0].ID != "first" {
		t.Fatalf("Read() = %+v, want the first report", got)
	}
	modified := time.Now().Add(-time.Hour)
	if err := os.Chtimes(file, modified, modified); err != nil {
		t.Fatal(err)
	}
	addOns.Read(now)

	// A report of the same size and modification time is not read again.
	write("other")
	if err := os.Chtimes(file, modified, modified); err != nil {
		t.Fatal(err)
	}
	if got := addOns.Read(now); len(got) != 1 || got[0].ID != "first" {
		t.Errorf("Read() of an unchanged file = %+v, want the first report kept", got)
	}

	// A newer one is.
	if err := os.Chtimes(file, now, now); err != nil {
		t.Fatal(err)
	}
	if got := addOns.Read(now); len(got) != 1 || got[0].ID != "other" {
		t.Errorf("Read() of a changed file = %+v, want the other report", got)
	}
}

func TestAddOnsWithoutFolderReadNothing(t *testing.T) {
	var none *AddOns
	if got := none.Read(time.Now()); got != nil {
		t.Errorf("nil Read() = %v, want nil", got)
	}
	if got := (&AddOns{Dir: filepath.Join(t.TempDir(), "missing"), MaxEntries: 64}).Read(time.Now()); got != nil {
		t.Errorf("Read() of a missing folder = %v, want nil", got)
	}
}

func TestWriteAddOnReportLeavesNoTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	if err := WriteAddOnReport(filepath.Join(dir, "power.json"), AddOnReport{Time: time.Now()}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "power.json" {
		t.Errorf("folder holds %v, want only power.json", entries)
	}
}
