package smart

import (
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"unsafe"
)

func TestKernelStructsHaveTheKernelsSize(t *testing.T) {
	if size := unsafe.Sizeof(nvmeAdminCmd{}); size != 72 {
		t.Errorf("nvme_passthru_cmd has %d bytes, want 72", size)
	}
	want := uintptr(64)
	if unsafe.Sizeof(uintptr(0)) == 8 {
		want = 88
	}
	if size := unsafe.Sizeof(sgIOHdr{}); size != want {
		t.Errorf("sg_io_hdr has %d bytes, want %d", size, want)
	}
}

// fakeSys builds a /sys with the given block devices, each a link into
// devices like the kernel's, and files below them.
func fakeSys(t *testing.T, links map[string]string, files map[string]string) string {
	t.Helper()
	sys := t.TempDir()
	names := slices.Sorted(maps.Keys(links))
	for _, name := range names {
		target := links[name]
		path := filepath.Join(sys, name)
		if err := os.MkdirAll(filepath.Join(sys, target), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(sys, target), path); err != nil {
			t.Fatal(err)
		}
	}
	for name, text := range files {
		path := filepath.Join(sys, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return sys
}

func TestListKeepsFixedSATAAndNVMeDisks(t *testing.T) {
	pci := "devices/pci0000:00/0000:00:17.0"
	sys := fakeSys(t, map[string]string{
		"block/sda":            pci + "/ata1/host0/target0:0:0/0:0:0:0/block/sda",
		"block/sda/device":     pci + "/ata1/host0/target0:0:0/0:0:0:0",
		"block/sdb":            pci + "/usb1/1-1/host1/target1:0:0/1:0:0:0/block/sdb",
		"block/nvme0n1":        pci + "/nvme/nvme0/nvme0n1",
		"block/nvme0n1/device": pci + "/nvme/nvme0",
		"block/nvme0n2":        pci + "/nvme/nvme0/nvme0n2",
		"block/nvme0n2/device": pci + "/nvme/nvme0",
		"block/nvme1n1":        "devices/virtual/nvme-subsystem/nvme-subsys1/nvme1n1",
		"block/nvme1n1/device": "devices/virtual/nvme-subsystem/nvme-subsys1",
		"devices/virtual/nvme-subsystem/nvme-subsys1/nvme1": pci + "/nvme/nvme1",
		"block/loop0":   "devices/virtual/block/loop0",
		"block/zram0":   "devices/virtual/block/zram0",
		"block/mmcblk0": "devices/platform/mmc/mmcblk0",
		"block/sdc":     "devices/virtual/block/sdc",
	}, map[string]string{
		"block/sda/removable":     "0",
		"block/sda/device/model":  "WDC WD40EFRX-68N",
		"block/sdb/removable":     "1",
		"block/nvme0n1/removable": "0",
		"class/nvme/nvme0/model":  "Samsung SSD 980 PRO 1TB",
	})
	src := linuxSource{sys: sys, dev: "/dev"}

	got, err := src.list()
	if err != nil {
		t.Fatal(err)
	}

	want := []device{
		{name: "nvme0", path: "/dev/nvme0", nvme: true, model: "Samsung SSD 980 PRO 1TB"},
		{name: "nvme1", path: "/dev/nvme1", nvme: true},
		{name: "sda", path: "/dev/sda", model: "WDC WD40EFRX-68N"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("list() = %+v, want %+v", got, want)
	}
}

func TestControllerOfAMultipathNamespace(t *testing.T) {
	sys := fakeSys(t, map[string]string{
		"block/nvme0n1/device":                              "devices/virtual/nvme-subsystem/nvme-subsys0",
		"devices/virtual/nvme-subsystem/nvme-subsys0/nvme1": "devices/pci0000:00/nvme/nvme1",
	}, nil)
	src := linuxSource{sys: sys}

	if got := src.controller(filepath.Join(sys, "block/nvme0n1/device"), "nvme0"); got != "nvme1" {
		t.Errorf("controller() = %q, want nvme1 from the subsystem", got)
	}
	if got := src.controller(filepath.Join(sys, "missing"), "nvme0"); got != "nvme0" {
		t.Errorf("controller() = %q, want the guess nvme0", got)
	}
}

func TestListWithoutSysFails(t *testing.T) {
	if _, err := (linuxSource{sys: t.TempDir()}).list(); err == nil {
		t.Error("list() without /sys/block did not fail")
	}
}
