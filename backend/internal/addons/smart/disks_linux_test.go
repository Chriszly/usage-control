package smart

import (
	"errors"
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
		"class/nvme/nvme0/serial": "S5GXNF0R123456A",
	})
	src := linuxSource{sys: sys, dev: "/dev"}

	got, err := src.list()
	if err != nil {
		t.Fatal(err)
	}

	want := []device{
		{name: "nvme0", path: "/dev/nvme0", nvme: true, model: "Samsung SSD 980 PRO 1TB", serial: "S5GXNF0R123456A", blocks: []string{"nvme0n1", "nvme0n2"}},
		{name: "nvme1", path: "/dev/nvme1", nvme: true, blocks: []string{"nvme1n1"}},
		{name: "sda", path: "/dev/sda", model: "WDC WD40EFRX-68N", blocks: []string{"sda"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("list() = %+v, want %+v", got, want)
	}
}

func TestListLeavesOutUSBDisks(t *testing.T) {
	// A Raspberry Pi 5 with a disk on a SATA card on its PCIe port, a USB
	// disk that is not removable (UAS) and an NVMe disk in a USB case,
	// which shows as a SCSI disk too; and a Raspberry Pi 4's USB disk.
	pi5 := "devices/platform/axi/1000110000.pcie/pci0000:00/0000:00:00.0/0000:01:00.0"
	usb := "devices/platform/axi/1000120000.pcie/1f00300000.usb/xhci-hcd.1"
	sys := fakeSys(t, map[string]string{
		"block/sda": pi5 + "/ata1/host0/target0:0:0/0:0:0:0/block/sda",
		"block/sdb": usb + "/usb4/4-1/4-1:1.0/host1/target1:0:0/1:0:0:0/block/sdb",
		"block/sdc": usb + "/usb2/2-2/2-2:1.0/host2/target2:0:0/2:0:0:0/block/sdc",
		"block/sdd": "devices/platform/scb/fd500000.pcie/pci0000:00/0000:00:00.0/0000:01:00.0/usb2/2-1/2-1:1.0/host3/target3:0:0/3:0:0:0/block/sdd",
	}, map[string]string{
		"block/sda/removable": "0",
		"block/sdb/removable": "0",
		"block/sdc/removable": "0",
		"block/sdd/removable": "0",
	})

	got, err := (linuxSource{sys: sys, dev: "/dev"}).list()
	if err != nil {
		t.Fatal(err)
	}
	if want := []device{{name: "sda", path: "/dev/sda", blocks: []string{"sda"}}}; !reflect.DeepEqual(got, want) {
		t.Errorf("list() = %+v, want %+v, without the USB disks", got, want)
	}
}

func TestSGAnswerChecksWhatCameBack(t *testing.T) {
	sector := make([]byte, 512)
	sector[0] = 1
	// The registers of a passed SMART check, as descriptor-format sense data.
	passed := []byte{
		0x72, 0x01, 0x00, 0x1D, 0, 0, 0, 14,
		0x09, 0x0C, 0, 0x00, 0, 0x00, 0, 0x00, 0, 0x4F, 0, 0xC2, 0xA0, 0x50,
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	}
	// Fixed-format sense data with the sense keys RECOVERED ERROR and MEDIUM
	// ERROR.
	recovered := []byte{0x70, 0, 0x01, 0, 0, 0, 0, 10, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	medium := []byte{0x70, 0, 0x03, 0, 0, 0, 0, 10, 0, 0, 0, 0, 0x11, 0, 0, 0, 0, 0}
	tests := []struct {
		name    string
		command ataCommand
		hdr     sgIOHdr
		sense   []byte
		ok      bool
	}{
		{"a full sector", ataSMARTReadData, sgIOHdr{}, nil, true},
		{"an empty transfer", ataSMARTReadData, sgIOHdr{resid: 512}, nil, false},
		{"half a sector", ataIdentify, sgIOHdr{resid: 256}, nil, false},
		{"a SCSI error", ataIdentify, sgIOHdr{status: scsiCheckCondition}, nil, false},
		{"a host error", ataIdentify, sgIOHdr{hostStatus: 0x07}, nil, false},
		{"a driver timeout", ataIdentify, sgIOHdr{driverStatus: 0x06}, nil, false},
		{"registers with sense data", ataSMARTReturnStatus, sgIOHdr{status: scsiCheckCondition, driverStatus: sgDriverSense, sbLenWr: 22}, passed, true},
		{"a driver error with sense data", ataSMARTReturnStatus, sgIOHdr{status: scsiCheckCondition, driverStatus: sgDriverSense | 0x04, sbLenWr: 22}, passed, false},
		{"no registers", ataSMARTReturnStatus, sgIOHdr{}, nil, false},
		{"a sector with RECOVERED ERROR", ataIdentify, sgIOHdr{status: scsiCheckCondition, driverStatus: sgDriverSense, sbLenWr: 18}, recovered, true},
		{"a sector with MEDIUM ERROR", ataIdentify, sgIOHdr{status: scsiCheckCondition, driverStatus: sgDriverSense, sbLenWr: 18}, medium, false},
	}
	for _, test := range tests {
		sense := make([]byte, 32)
		copy(sense, test.sense)
		result, data, err := sgAnswer(test.command, &test.hdr, sense, sector)
		switch {
		case test.ok != (err == nil):
			t.Errorf("%s: sgAnswer() = %v, want ok %v", test.name, err, test.ok)
		case err == nil && test.command.dataIn && len(data) != 512:
			t.Errorf("%s: sgAnswer() = %d bytes, want the sector", test.name, len(data))
		case err == nil && !test.command.dataIn && (smartPassed(result) == nil || !*smartPassed(result)):
			t.Errorf("%s: sgAnswer() = %+v, want the registers of a passed check", test.name, result)
		}
	}
}

func TestSGAnswerTellsARefusalFromOtherErrors(t *testing.T) {
	// The registers of an aborted command: ERR in the status, ABRT in the
	// error register.
	aborted := []byte{
		0x72, 0x01, 0x00, 0x1D, 0, 0, 0, 14,
		0x09, 0x0C, 0, 0x04, 0, 0x00, 0, 0x00, 0, 0x00, 0, 0x00, 0xA0, 0x51,
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	}
	sense := make([]byte, 32)
	copy(sense, aborted)
	hdr := sgIOHdr{status: scsiCheckCondition, driverStatus: sgDriverSense, sbLenWr: 22}
	if _, _, err := sgAnswer(ataSMARTReadData, &hdr, sense, make([]byte, 512)); !errors.Is(err, errRefused) {
		t.Errorf("sgAnswer() = %v, want errRefused for the ERR bit", err)
	}
	for _, hdr := range []sgIOHdr{{driverStatus: 0x06}, {resid: 512}, {hostStatus: 0x07}} {
		if _, _, err := sgAnswer(ataSMARTReadData, &hdr, make([]byte, 32), make([]byte, 512)); err == nil || errors.Is(err, errRefused) {
			t.Errorf("sgAnswer(%+v) = %v, want an error that is not a refusal", hdr, err)
		}
	}
}

func TestIOCountAddsUpTheBlockDevices(t *testing.T) {
	sys := fakeSys(t, nil, map[string]string{
		"block/nvme0n1/stat":          "  120  3  9000  50  80  1  700  30  0  60  90  0  0  0  0  4  2",
		"block/nvme0n2/stat":          "    5  0    40   1   2  0   16   1  0   2   2",
		"block/sda/stat":              "garbage",
		"block/sdb/stat":              "  120  3  9000  50  80  1  700  30  0  60  90",
		"block/sdb/queue/iostats":     "0",
		"block/nvme0n1/queue/iostats": "1",
	})
	src := linuxSource{sys: sys}

	if got, ok := src.ioCount(device{blocks: []string{"nvme0n1", "nvme0n2"}}); !ok || got != 207 {
		t.Errorf("ioCount() = %d, %v, want 207 reads and writes", got, ok)
	}
	// sda's stat cannot be read, sdz is gone and sdb does not count.
	for _, blocks := range [][]string{{"sda"}, {"sdz"}, {"sdb"}, nil} {
		if got, ok := src.ioCount(device{blocks: blocks}); ok {
			t.Errorf("ioCount() of %v = %d, want not counted", blocks, got)
		}
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
