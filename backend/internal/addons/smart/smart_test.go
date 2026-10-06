package smart

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	out, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // the test's own testdata
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestParseScanKeepsDisksSmartctlCanOpen(t *testing.T) {
	got, err := parseScan(readTestdata(t, "scan.json"))
	if err != nil {
		t.Fatal(err)
	}

	want := []device{
		{name: "/dev/sda", kind: "sat"},
		{name: "/dev/nvme0", kind: "nvme"},
		{name: "/dev/bus/0", kind: "megaraid,0"},
		{name: "/dev/bus/0", kind: "megaraid,1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseScan() = %+v, want %+v", got, want)
	}
}

func TestParseScanFailsOnOtherOutput(t *testing.T) {
	if _, err := parseScan([]byte("smartctl: unrecognized option")); err == nil {
		t.Error("parseScan() of text did not fail")
	}
}

func ptr(f float64) *float64 { return &f }

func TestParseDiskReadsSATA(t *testing.T) {
	got, ok := parseDisk(readTestdata(t, "sata.json"))

	passed := true
	want := Disk{
		Model: "WDC WD40EFRX-68N32N0", Passed: &passed, Celsius: ptr(34),
		PowerOnHours: ptr(35215), ReallocatedSectors: ptr(8),
	}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("parseDisk() = %+v, %v, want %+v", got, ok, want)
	}
}

func TestParseDiskReadsNVMeThatFails(t *testing.T) {
	got, ok := parseDisk(readTestdata(t, "nvme.json"))

	passed := false
	want := Disk{
		Model: "Samsung SSD 980 PRO 1TB", Passed: &passed, Celsius: ptr(41),
		PowerOnHours: ptr(6120), MediaErrors: ptr(0), PercentageUsed: ptr(3),
	}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("parseDisk() = %+v, %v, want %+v", got, ok, want)
	}
}

func TestParseDiskSkipsASleepingDisk(t *testing.T) {
	if got, ok := parseDisk(readTestdata(t, "standby.json")); ok {
		t.Errorf("parseDisk() = %+v, want nothing for a disk in standby", got)
	}
	if got, ok := parseDisk(nil); ok {
		t.Errorf("parseDisk(nil) = %+v, want nothing", got)
	}
}

func TestExtrasLabelsEachValueWithItsDisk(t *testing.T) {
	passed := false
	got := Extras([]Disk{
		{Name: "/dev/nvme0", Model: "Samsung SSD 980", Passed: &passed, Celsius: ptr(41), PercentageUsed: ptr(3)},
		{Name: "/dev/bus/0 megaraid,1", PowerOnHours: ptr(10)},
	})

	if len(got) != 1 || got[0].ID != "smart" || got[0].Title != "Disk health" || got[0].Titles["de"] != "Laufwerkszustand" {
		t.Fatalf("Extras() = %+v, want one group Disk health", got)
	}
	items := got[0].Items
	var ids []string
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	wantIDs := []string{"nvme0-health", "nvme0-temperature", "nvme0-used", "bus-0-megaraid-1-power-on-hours"}
	if !reflect.DeepEqual(ids, wantIDs) {
		t.Errorf("ids = %v, want %v", ids, wantIDs)
	}
	health := items[0]
	if health.Unit != metrics.UnitText || health.Text != "FAILED" || health.History ||
		health.Label != "Samsung SSD 980 (nvme0): Health" || health.Labels["fr"] != "Samsung SSD 980 (nvme0): État" {
		t.Errorf("health = %+v, want FAILED as text without history", health)
	}
	if temp := items[1]; temp.Unit != metrics.UnitCelsius || *temp.Value != 41 || !temp.History ||
		temp.Labels["es"] != "Samsung SSD 980 (nvme0): Temperatura" {
		t.Errorf("temperature = %+v, want 41 °C with history", temp)
	}
	if hours := items[3]; hours.Label != "bus/0 megaraid,1: Power-on hours" {
		t.Errorf("label = %q, want the device without a model", hours.Label)
	}
	if clean := metrics.CleanExtras(got, 64); !reflect.DeepEqual(clean, got) {
		t.Errorf("CleanExtras() changed the extras: %+v", clean)
	}
}

func TestExtrasOfNoDisksIsNothing(t *testing.T) {
	if got := Extras(nil); got != nil {
		t.Errorf("Extras(nil) = %+v, want nil", got)
	}
	if got := Extras([]Disk{{Name: "/dev/sda"}}); got != nil {
		t.Errorf("Extras() of a disk without values = %+v, want nil", got)
	}
}

func TestIDOfKeepsRoomForTheLongestValue(t *testing.T) {
	id := idOf(strings.Repeat("disk", 20)) + "-power-on-hours"
	if len(id) > 40 {
		t.Errorf("id %q is longer than 40 characters", id)
	}
}

// fakeSmartctl answers like smartctl from testdata and counts the calls.
type fakeSmartctl struct {
	mu     sync.Mutex
	asleep bool
	calls  []string
	files  map[string][]byte
}

func (f *fakeSmartctl) run(_ context.Context, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.Join(args, " "))
	if args[0] == "--scan-open" {
		return f.files["scan"], nil
	}
	if f.asleep && args[len(args)-1] == "/dev/sda" {
		return f.files["standby"], nil
	}
	return f.files[args[len(args)-1]], nil
}

func (f *fakeSmartctl) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func TestReaderReadsEveryTenMinutesAndKeepsTheLastResult(t *testing.T) {
	fake := &fakeSmartctl{files: map[string][]byte{
		"scan":       []byte(`{"devices":[{"name":"/dev/sda","type":"sat"},{"name":"/dev/nvme0","type":"nvme"}]}`),
		"standby":    readTestdata(t, "standby.json"),
		"/dev/sda":   readTestdata(t, "sata.json"),
		"/dev/nvme0": readTestdata(t, "nvme.json"),
	}}
	r := newReader(fake.run)
	start := time.Now()
	ctx := context.Background()

	r.refresh(ctx, start)
	got := r.Read(ctx, start.Add(5*time.Second))
	if len(got) != 2 || got[0].Name != "/dev/sda" || got[1].Name != "/dev/nvme0" {
		t.Fatalf("Read() = %+v, want sda and nvme0", got)
	}
	if fake.calls[1] != "--json --all --nocheck=standby --device=sat /dev/sda" {
		t.Errorf("call = %q, want a read that skips a sleeping disk", fake.calls[1])
	}
	if n := fake.count(); n != 3 {
		t.Errorf("smartctl ran %d times before ten minutes passed, want 3", n)
	}

	// Ten minutes later sda sleeps: it keeps its last result and the disks
	// are not listed again before an hour.
	fake.asleep = true
	r.refresh(ctx, start.Add(ReadInterval))
	got = r.Read(ctx, start.Add(ReadInterval+5*time.Second))
	if len(got) != 2 || *got[0].Celsius != 34 {
		t.Errorf("Read() = %+v, want the sleeping disk's last result", got)
	}
	if n := fake.count(); n != 5 {
		t.Errorf("smartctl ran %d times, want 5 (no new scan)", n)
	}
}

func TestReaderStartsAReadInTheBackground(t *testing.T) {
	fake := &fakeSmartctl{files: map[string][]byte{
		"scan":     []byte(`{"devices":[{"name":"/dev/sda","type":"sat"}]}`),
		"/dev/sda": readTestdata(t, "sata.json"),
	}}
	r := newReader(fake.run)
	now := time.Now()

	if got := r.Read(context.Background(), now); len(got) != 0 {
		t.Errorf("first Read() = %+v, want nothing before the first read ends", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(r.Read(context.Background(), now)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the background read did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	if n := fake.count(); n != 2 {
		t.Errorf("smartctl ran %d times, want 2", n)
	}
}

func TestNilReaderReadsNothing(t *testing.T) {
	var r *Reader
	if got := r.Read(context.Background(), time.Now()); got != nil {
		t.Errorf("Read() = %+v, want nil", got)
	}
}
