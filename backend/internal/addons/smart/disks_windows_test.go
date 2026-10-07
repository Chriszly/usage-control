package smart

import (
	"errors"
	"testing"
	"unsafe"
)

func TestWindowsATAPassThroughHasItsSize(t *testing.T) {
	if size := unsafe.Sizeof(ataPassThroughEx{}); size != 48 {
		t.Errorf("ATA_PASS_THROUGH_EX has %d bytes, want 48", size)
	}
}

// Lists and reads this machine's disks. It needs administrator rights to
// read them, which the Windows runner has; it only logs what it read, as
// the runner's disks may report nothing.
func TestWindowsReadsTheDisks(t *testing.T) {
	src := windowsSource{}
	devices, err := src.list()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range devices {
		disk, err := src.read(d)
		switch {
		case errors.Is(err, errAsleep):
			t.Logf("%s (%s): asleep", d.name, d.model)
		case err != nil:
			t.Logf("%s (%s): %v", d.name, d.model, err)
		default:
			disk.Name, disk.Model = d.name, d.model
			t.Logf("%+v", Extras([]Disk{disk}))
		}
	}
}
