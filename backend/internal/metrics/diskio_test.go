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
		{179, 0}: {read: 726334 * 512, written: 271096 * 512},
		{179, 2}: {read: 709098 * 512, written: 271096 * 512},
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

func TestDiskSpeedsMeasureSincePreviousReading(t *testing.T) {
	disks := []Disk{{Path: "/"}, {Path: "/mnt/usb"}, {Path: "/mnt/share"}}
	previous := map[string]ioCounters{"/": {read: 1000, written: 500}}
	current := map[string]ioCounters{
		"/":        {read: 5000, written: 1500},
		"/mnt/usb": {read: 300, written: 400}, // plugged in since the previous reading
	}

	diskSpeeds(previous, current, 2*time.Second, disks)

	speeds := func(d Disk) []float64 {
		if d.ReadBytesPerSecond == nil || d.WriteBytesPerSecond == nil {
			return nil
		}
		return []float64{*d.ReadBytesPerSecond, *d.WriteBytesPerSecond}
	}
	if got := speeds(disks[0]); !reflect.DeepEqual(got, []float64{2000, 500}) {
		t.Errorf("speeds of / = %v, want [2000 500]", got)
	}
	if got := speeds(disks[1]); !reflect.DeepEqual(got, []float64{0, 0}) {
		t.Errorf("speeds of a new disk = %v, want [0 0]", got)
	}
	if got := speeds(disks[2]); got != nil {
		t.Errorf("speeds of a disk without counters = %v, want none", got)
	}
}
