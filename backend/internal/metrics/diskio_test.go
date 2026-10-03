package metrics

import (
	"reflect"
	"testing"
	"time"
)

func TestParseDiskstats(t *testing.T) {
	text := ` 179       0 mmcblk0 9132 3518 726334 12000 4561 6187 271096 30000 0 15000 42000 0 0 0 0 0 0
 179       2 mmcblk0p2 8833 3518 709098 11000 4561 6187 271096 30000 0 14000 41000 0 0 0 0 0 0
   1       0 ram0 0 0 0 0
not a line of numbers
`
	want := map[deviceNumber]ioCounters{
		{179, 0}: {read: 726334 * 512, written: 271096 * 512, operations: 13693, waitMs: 42000, busyMs: 15000, hasTimes: true},
		{179, 2}: {read: 709098 * 512, written: 271096 * 512, operations: 13394, waitMs: 41000, busyMs: 14000, hasTimes: true},
	}
	if got := parseDiskstats(text); !reflect.DeepEqual(got, want) {
		t.Errorf("parseDiskstats() = %v, want %v", got, want)
	}
}

func TestParseMountinfoRoot(t *testing.T) {
	text := `22 1 0:21 / /proc rw,nosuid shared:12 - proc proc rw
25 1 179:2 / / rw,noatime shared:1 - ext4 /dev/root rw
`
	got, ok := parseMountinfoRoot(text)
	if want := (deviceNumber{179, 2}); !ok || got != want {
		t.Errorf("parseMountinfoRoot() = %v, %v, want %v, true", got, ok, want)
	}
	if _, ok := parseMountinfoRoot("22 1 0:21 / /proc rw - proc proc rw\n"); ok {
		t.Error("parseMountinfoRoot() without a root mount = true, want false")
	}
}

func TestDiskActivityMeasuresSincePreviousReading(t *testing.T) {
	disks := []Disk{{Path: "/"}, {Path: "/mnt/usb"}, {Path: "/mnt/share"}, {Path: `C:\`}}
	previous := map[string]ioCounters{
		"/":   {read: 1000, written: 500, operations: 100, waitMs: 1000, busyMs: 3000, hasTimes: true},
		`C:\`: {read: 0, written: 0, operations: 10},
	}
	current := map[string]ioCounters{
		"/":        {read: 5000, written: 1500, operations: 140, waitMs: 1200, busyMs: 3500, hasTimes: true},
		"/mnt/usb": {read: 300, written: 400, operations: 5, hasTimes: true}, // plugged in since the previous reading
		`C:\`:      {read: 2000, written: 0, operations: 30},                 // Windows reports no times
	}

	diskActivity(previous, current, 2*time.Second, disks)

	values := func(d Disk) []float64 {
		var v []float64
		for _, p := range []*float64{d.ReadBytesPerSecond, d.WriteBytesPerSecond, d.OperationsPerSecond, d.BusyPercent, d.LatencyMs} {
			if p != nil {
				v = append(v, *p)
			}
		}
		return v
	}
	tests := []struct {
		disk Disk
		want []float64
	}{
		{disks[0], []float64{2000, 500, 20, 25, 5}},
		{disks[1], []float64{0, 0, 0, 0, 0}},
		{disks[2], nil},
		{disks[3], []float64{1000, 0, 10}},
	}
	for _, tt := range tests {
		if got := values(tt.disk); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("activity of %s = %v, want %v", tt.disk.Path, got, tt.want)
		}
	}
}
