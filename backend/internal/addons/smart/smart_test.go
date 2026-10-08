package smart

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
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

func ptr(f float64) *float64 { return &f }

func yes() *bool { b := true; return &b }

func no() *bool { b := false; return &b }

func TestParseNVMeHealthReadsTheLog(t *testing.T) {
	got, err := parseNVMeHealth(readTestdata(t, "nvme-health.bin"))
	if err != nil {
		t.Fatal(err)
	}

	want := Disk{Passed: yes(), Celsius: ptr(41), PowerOnHours: ptr(6120), MediaErrors: ptr(0), PercentageUsed: ptr(3)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseNVMeHealth() = %+v, want %+v", got, want)
	}
}

func TestParseNVMeHealthFailsOnACriticalWarning(t *testing.T) {
	log := readTestdata(t, "nvme-health.bin")
	log[0] = 0x04 // reliability degraded
	log[160] = 7  // media errors
	log[1], log[2] = 0, 0

	got, err := parseNVMeHealth(log)
	if err != nil {
		t.Fatal(err)
	}
	if *got.Passed || *got.MediaErrors != 7 || got.Celsius != nil {
		t.Errorf("parseNVMeHealth() = %+v, want FAILED, 7 media errors and no temperature", got)
	}
	if _, err := parseNVMeHealth(log[:100]); err == nil {
		t.Error("parseNVMeHealth() of a short log did not fail")
	}
}

func TestUint128ReadsTheHighHalf(t *testing.T) {
	b := make([]byte, 16)
	b[0], b[8] = 1, 1
	if got := uint128(b); got != 1+18446744073709551616 {
		t.Errorf("uint128() = %v", got)
	}
}

func TestParseSMARTDataReadsTheAttributes(t *testing.T) {
	got, err := parseSMARTData(readTestdata(t, "ata-smart.bin"))
	if err != nil {
		t.Fatal(err)
	}

	// 194 holds 34 °C with the lowest and highest in its other bytes.
	want := Disk{Celsius: ptr(34), PowerOnHours: ptr(35215), ReallocatedSectors: ptr(8)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseSMARTData() = %+v, want %+v", got, want)
	}
}

func TestParseSMARTDataFallsBackToTheAirflowTemperature(t *testing.T) {
	sector := readTestdata(t, "ata-smart.bin")
	i := bytes.IndexByte(sector[2:362], 194) + 2
	sector[i] = 0 // no attribute 194
	sector[511] += 194

	got, err := parseSMARTData(sector)
	if err != nil {
		t.Fatal(err)
	}
	if got.Celsius == nil || *got.Celsius != 36 {
		t.Errorf("Celsius = %v, want 36 from attribute 190", got.Celsius)
	}
}

func TestParseSMARTDataChecksTheChecksum(t *testing.T) {
	sector := readTestdata(t, "ata-smart.bin")
	sector[100]++
	if _, err := parseSMARTData(sector); !errors.Is(err, errChecksum) {
		t.Errorf("parseSMARTData() = %v, want a checksum error", err)
	}
	if _, err := parseSMARTData(sector[:511]); err == nil {
		t.Error("parseSMARTData() of a short sector did not fail")
	}
}

func TestParseIdentifyReadsTheModelAndSMART(t *testing.T) {
	sector := readTestdata(t, "ata-identify.bin")
	model, serial, smart, err := parseIdentify(sector)
	if err != nil || model != "WDC WD40EFRX-68N32N0" || serial != "WD-WCC7K0000000" || smart == nil || !*smart {
		t.Errorf("parseIdentify() = %q, %q, %v, %v, want the model and serial with SMART on", model, serial, smart, err)
	}

	// SMART switched off.
	sector[2*85] &^= 1
	sector[511]++
	if _, _, smart, err := parseIdentify(sector); err != nil || smart == nil || *smart {
		t.Errorf("parseIdentify() = %v, %v, want SMART off", smart, err)
	}
	sector[0]++
	if _, _, _, err := parseIdentify(sector); !errors.Is(err, errChecksum) {
		t.Errorf("parseIdentify() = %v, want a checksum error", err)
	}
}

func TestParseIdentifyReadsSMARTOnlyFromValidWords(t *testing.T) {
	for _, tt := range []struct {
		name string
		// clear are the words whose bit 0 is cleared, invalid those whose
		// bits 15:14 are cleared.
		clear, invalid []int
		want           *bool
	}{
		{name: "words 83 and 87 valid", want: yes()},
		{name: "word 87 not valid", invalid: []int{87}},
		{name: "word 83 not valid", invalid: []int{83}},
		{name: "neither valid, word 85 off", clear: []int{85}, invalid: []int{83, 87}},
		{name: "word 83 not valid, word 85 off", clear: []int{85}, invalid: []int{83}, want: no()},
		{name: "word 87 not valid, word 82 off", clear: []int{82}, invalid: []int{87}, want: no()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sector := readTestdata(t, "ata-identify.bin")
			sector[510] = 0 // no checksum
			for _, w := range tt.clear {
				sector[2*w] &^= 1
			}
			for _, w := range tt.invalid {
				sector[2*w+1] &^= 0xC0
			}
			_, _, smart, err := parseIdentify(sector)
			if err != nil || !reflect.DeepEqual(smart, tt.want) {
				t.Errorf("parseIdentify() = %v, %v, want %v", smart, err, tt.want)
			}
		})
	}
}

func TestATACommandsAsPassThroughCDBs(t *testing.T) {
	tests := []struct {
		command ataCommand
		want    []byte
	}{
		{ataCheckPowerMode, []byte{0x85, 0x06, 0x20, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xE5, 0}},
		{ataIdentify, []byte{0x85, 0x08, 0x0E, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0xEC, 0}},
		{ataSMARTReadData, []byte{0x85, 0x08, 0x0E, 0, 0xD0, 0, 1, 0, 0, 0, 0x4F, 0, 0xC2, 0, 0xB0, 0}},
		{ataSMARTReturnStatus, []byte{0x85, 0x06, 0x20, 0, 0xDA, 0, 0, 0, 0, 0, 0x4F, 0, 0xC2, 0, 0xB0, 0}},
	}
	for _, test := range tests {
		if got := test.command.cdb(); !bytes.Equal(got, test.want) {
			t.Errorf("cdb() of %#x = % x, want % x", test.command.command, got, test.want)
		}
	}
}

func TestParseATASenseReadsBothFormats(t *testing.T) {
	// Descriptor format: recovered error, ATA PASS-THROUGH INFORMATION
	// AVAILABLE, with the ATA Status Return descriptor: a failed SMART check.
	descriptor := []byte{
		0x72, 0x01, 0x00, 0x1D, 0, 0, 0, 14,
		0x09, 0x0C, 0, 0x00, 0, 0x00, 0, 0x00, 0, 0xF4, 0, 0x2C, 0xA0, 0x50,
	}
	got, ok := parseATASense(descriptor)
	if !ok || got.lbaMid != 0xF4 || got.lbaHigh != 0x2C || got.status != 0x50 {
		t.Errorf("parseATASense(descriptor) = %+v, %v", got, ok)
	}
	if passed := smartPassed(got); passed == nil || *passed {
		t.Errorf("smartPassed() = %v, want false", passed)
	}

	// Fixed format: CHECK POWER MODE answered standby.
	fixed := make([]byte, 18)
	fixed[0], fixed[2], fixed[4], fixed[6], fixed[12], fixed[13] = 0x70, 0x01, 0x50, 0x00, 0x00, 0x1D
	got, ok = parseATASense(fixed)
	if !ok || !asleep(got) {
		t.Errorf("parseATASense(fixed) = %+v, %v, want standby", got, ok)
	}

	// Other sense data, or none.
	fixed[13] = 0x00
	if _, ok := parseATASense(fixed); ok {
		t.Error("parseATASense() read registers from other sense data")
	}
	if _, ok := parseATASense(nil); ok {
		t.Error("parseATASense(nil) read registers")
	}
}

func TestSMARTPassedKnowsOnlyTheTwoAnswers(t *testing.T) {
	if passed := smartPassed(ataResult{lbaMid: 0x4F, lbaHigh: 0xC2}); passed == nil || !*passed {
		t.Errorf("smartPassed() = %v, want true", passed)
	}
	if passed := smartPassed(ataResult{}); passed != nil {
		t.Errorf("smartPassed() = %v, want nil", *passed)
	}
}

// fakeATA answers ATA commands like a SATA disk, from testdata.
type fakeATA struct {
	t      *testing.T
	power  byte
	passed bool
	// smartOff switches SMART off in the identify data, unknown marks its
	// words 83 and 87 as not valid, refuse makes the disk refuse to send its
	// attributes, timeout makes it not answer, and badChecksum spoils their
	// checksum.
	smartOff, unknown, refuse, timeout, badChecksum bool
	sent                                   []byte
}

func (f *fakeATA) send(c ataCommand) (ataResult, []byte, error) {
	f.sent = append(f.sent, c.command)
	switch {
	case c == ataCheckPowerMode:
		return ataResult{count: f.power}, nil, nil
	case c == ataIdentify:
		sector := readTestdata(f.t, "ata-identify.bin")
		if f.smartOff {
			sector[2*85] &^= 1
			sector[511]++
		}
		if f.unknown {
			sector[2*83+1] &^= 0xC0
			sector[2*87+1] &^= 0xC0
			sector[511] += 0x80
		}
		return ataResult{}, sector, nil
	case c == ataSMARTReadData && f.refuse:
		return ataResult{}, nil, fmt.Errorf("%w 0xb0 (error 0x4)", errRefused)
	case c == ataSMARTReadData && f.timeout:
		return ataResult{}, nil, errors.New("SG_IO driver status 0x6")
	case c == ataSMARTReadData:
		sector := readTestdata(f.t, "ata-smart.bin")
		if f.badChecksum {
			sector[100]++
		}
		return ataResult{}, sector, nil
	case c == ataSMARTReturnStatus && f.passed:
		return ataResult{lbaMid: 0x4F, lbaHigh: 0xC2}, nil, nil
	case c == ataSMARTReturnStatus:
		return ataResult{lbaMid: 0xF4, lbaHigh: 0x2C}, nil, nil
	}
	return ataResult{}, nil, errors.New("unknown command")
}

func TestReadATAReadsADiskThatIsAwake(t *testing.T) {
	disk := &fakeATA{t: t, power: 0xFF, passed: true}
	got, err := readATA(disk.send)
	if err != nil {
		t.Fatal(err)
	}

	want := Disk{
		Model: "WDC WD40EFRX-68N32N0", Serial: "WD-WCC7K0000000",
		Passed: yes(), Celsius: ptr(34), PowerOnHours: ptr(35215), ReallocatedSectors: ptr(8),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("readATA() = %+v, want %+v", got, want)
	}
	if !bytes.Equal(disk.sent, []byte{0xE5, 0xEC, 0xB0, 0xB0}) {
		t.Errorf("sent % x, want CHECK POWER MODE first", disk.sent)
	}
}

func TestReadATAShowsADiskWithSMARTOff(t *testing.T) {
	disk := &fakeATA{t: t, power: 0xFF, smartOff: true}
	got, err := readATA(disk.send)

	want := Disk{Model: "WDC WD40EFRX-68N32N0", Serial: "WD-WCC7K0000000", SMARTOff: true}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("readATA() = %+v, %v, want %+v", got, err, want)
	}
	if !bytes.Equal(disk.sent, []byte{0xE5, 0xEC}) {
		t.Errorf("sent % x, want no SMART command", disk.sent)
	}
}

func TestReadATAReadsSMARTOfADiskThatDoesNotTell(t *testing.T) {
	disk := &fakeATA{t: t, power: 0xFF, passed: true, unknown: true}
	got, err := readATA(disk.send)

	want := Disk{
		Model: "WDC WD40EFRX-68N32N0", Serial: "WD-WCC7K0000000",
		Passed: yes(), Celsius: ptr(34), PowerOnHours: ptr(35215), ReallocatedSectors: ptr(8),
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("readATA() = %+v, %v, want %+v", got, err, want)
	}

	// One that then refuses to send its attributes shows SMART off.
	disk = &fakeATA{t: t, power: 0xFF, unknown: true, refuse: true}
	got, err = readATA(disk.send)
	want = Disk{Model: "WDC WD40EFRX-68N32N0", Serial: "WD-WCC7K0000000", SMARTOff: true}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("readATA() = %+v, %v, want %+v", got, err, want)
	}

	// One that does not answer, such as after a timeout, is an error, not
	// SMART off.
	disk = &fakeATA{t: t, power: 0xFF, unknown: true, timeout: true}
	if got, err := readATA(disk.send); err == nil {
		t.Errorf("readATA() = %+v, want an error after a timeout", got)
	}

	// One with SMART on that refuses is an error, not SMART off.
	disk = &fakeATA{t: t, power: 0xFF, refuse: true}
	if got, err := readATA(disk.send); err == nil {
		t.Errorf("readATA() = %+v, want an error", got)
	}
}

func TestReadATAKeepsTheCheckOfAttributesWithAWrongChecksum(t *testing.T) {
	disk := &fakeATA{t: t, power: 0xFF, passed: true, badChecksum: true}
	got, err := readATA(disk.send)

	want := Disk{Model: "WDC WD40EFRX-68N32N0", Serial: "WD-WCC7K0000000", Passed: yes()}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("readATA() = %+v, %v, want %+v without the attributes", got, err, want)
	}
}

func TestReadATALeavesASleepingDiskAlone(t *testing.T) {
	disk := &fakeATA{t: t, power: 0x00}
	if _, err := readATA(disk.send); !errors.Is(err, errAsleep) {
		t.Errorf("readATA() = %v, want errAsleep", err)
	}
	if !bytes.Equal(disk.sent, []byte{0xE5}) {
		t.Errorf("sent % x, want only CHECK POWER MODE", disk.sent)
	}
}

func TestReadATAWithoutPowerModeReadsNothing(t *testing.T) {
	var sent int
	_, err := readATA(func(ataCommand) (ataResult, []byte, error) {
		sent++
		return ataResult{}, nil, errors.New("not passed through")
	})
	if err == nil || sent != 1 {
		t.Errorf("readATA() = %v after %d commands, want an error after 1", err, sent)
	}
}

func TestDeviceDescriptorReadsModelBusAndRemovable(t *testing.T) {
	b := make([]byte, 128)
	binary.LittleEndian.PutUint32(b[0:], 128)
	binary.LittleEndian.PutUint32(b[4:], 100)
	binary.LittleEndian.PutUint32(b[16:], 64)
	binary.LittleEndian.PutUint32(b[24:], 90)
	binary.LittleEndian.PutUint32(b[28:], busNVMe)
	copy(b[64:], "Samsung SSD 980 PRO 1TB  \x00")
	copy(b[90:], "S69ENX0T1\x00")

	got, err := parseDeviceDescriptor(b)
	if err != nil || got != (storageDevice{model: "Samsung SSD 980 PRO 1TB", serial: "S69ENX0T1", bus: busNVMe}) {
		t.Errorf("parseDeviceDescriptor() = %+v, %v", got, err)
	}

	// Without a serial number, and an offset beyond the descriptor.
	b[10] = 1
	binary.LittleEndian.PutUint32(b[12:], 40)
	binary.LittleEndian.PutUint32(b[24:], 0xFFFFFFFF)
	copy(b[40:], "SanDisk\x00")
	copy(b[64:], "Ultra Fit\x00")
	binary.LittleEndian.PutUint32(b[28:], busUSB)
	got, err = parseDeviceDescriptor(b)
	if err != nil || got != (storageDevice{model: "SanDisk Ultra Fit", bus: busUSB, removable: true}) {
		t.Errorf("parseDeviceDescriptor() = %+v, %v", got, err)
	}
	if _, err := parseDeviceDescriptor(b[:20]); err == nil {
		t.Error("parseDeviceDescriptor() of a short buffer did not fail")
	}
}

func TestNVMeLogQueryAndAnswer(t *testing.T) {
	q := nvmeLogQuery(nvmeHealthLog, nvmeHealthLogSize)
	if len(q) != 8+40+512 {
		t.Fatalf("len = %d, want 560", len(q))
	}
	for offset, want := range map[int]uint32{0: 50, 4: 0, 8: 3, 12: 2, 16: 2, 20: 0, 24: 40, 28: 512} {
		if got := binary.LittleEndian.Uint32(q[offset:]); got != want {
			t.Errorf("dword at %d = %d, want %d", offset, got, want)
		}
	}

	// The driver answers in the same buffer, with the log after the
	// protocol data.
	log := readTestdata(t, "nvme-health.bin")
	copy(q[48:], log)
	got, err := nvmeLogFromDescriptor(q, nvmeHealthLogSize)
	if err != nil || !bytes.Equal(got, log) {
		t.Errorf("nvmeLogFromDescriptor() = %v", err)
	}
	if _, err := nvmeLogFromDescriptor(q[:300], nvmeHealthLogSize); err == nil {
		t.Error("nvmeLogFromDescriptor() of a short answer did not fail")
	}
}

func TestSendCmdLayout(t *testing.T) {
	in := sendCmdIn(ataSMARTReadData)
	want := []byte{0, 2, 0, 0, 0xD0, 1, 0, 0x4F, 0xC2, 0xA0, 0xB0, 0}
	if len(in) != 32 || !bytes.Equal(in[:12], want) {
		t.Errorf("sendCmdIn() = % x, want % x and 32 bytes", in, want)
	}

	out := make([]byte, sendCmdOutSize(ataSMARTReturnStatus))
	copy(out[16:], []byte{0, 0, 0, 0x4F, 0xC2, 0xA0, 0x50, 0})
	got, _, err := parseSendCmdOut(ataSMARTReturnStatus, out)
	if err != nil || smartPassed(got) == nil || !*smartPassed(got) {
		t.Errorf("parseSendCmdOut() = %+v, %v, want passed", got, err)
	}
	out[4] = 1
	if _, _, err := parseSendCmdOut(ataSMARTReturnStatus, out); !errors.Is(err, errRefused) {
		t.Errorf("parseSendCmdOut() = %v, want the disk's refusal", err)
	}
	out[4] = 9 // SMART_NOT_SUPPORTED
	if _, _, err := parseSendCmdOut(ataSMARTReturnStatus, out); !errors.Is(err, errDriver) || errors.Is(err, errRefused) {
		t.Errorf("parseSendCmdOut() = %v, want the driver's error", err)
	}
	sector := make([]byte, sendCmdOutSize(ataSMARTReadData))
	copy(sector[16:], readTestdata(t, "ata-smart.bin"))
	if _, data, err := parseSendCmdOut(ataSMARTReadData, sector); err != nil || len(data) != 512 {
		t.Errorf("parseSendCmdOut() = %d bytes, %v, want the sector", len(data), err)
	}
}

func TestExtrasLabelsEachValueWithItsDisk(t *testing.T) {
	got := Extras([]Disk{
		{Name: "nvme0", Model: "Samsung SSD 980", Serial: "S64DNX0R1", Passed: no(), Celsius: ptr(41), PercentageUsed: ptr(3)},
		{Name: "Disk 1", Passed: yes(), PowerOnHours: ptr(10)},
	})

	if len(got) != 1 || got[0].ID != "smart" || got[0].Title != "Disk health" || got[0].Titles["de"] != "Laufwerkszustand" {
		t.Fatalf("Extras() = %+v, want one group Disk health", got)
	}
	items := got[0].Items
	var ids []string
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	wantIDs := []string{"s64dnx0r1-health", "s64dnx0r1-temperature", "s64dnx0r1-used", "disk-1-health", "disk-1-power-on-hours"}
	if !reflect.DeepEqual(ids, wantIDs) {
		t.Errorf("ids = %v, want %v", ids, wantIDs)
	}
	failed := items[0]
	if failed.Unit != metrics.UnitText || failed.Text != "✗" || failed.History ||
		failed.Label != "Samsung SSD 980 (nvme0): SMART check FAILED" ||
		failed.Labels["de"] != "Samsung SSD 980 (nvme0): SMART-Prüfung NICHT BESTANDEN" ||
		failed.Labels["fr"] != "Samsung SSD 980 (nvme0): Contrôle SMART ÉCHOUÉ" ||
		failed.Labels["es"] != "Samsung SSD 980 (nvme0): Comprobación SMART FALLIDA" {
		t.Errorf("health = %+v, want FAILED in each language, as text without history", failed)
	}
	if passed := items[3]; passed.Text != "✓" || passed.Label != "Disk 1: SMART check passed" ||
		passed.Labels["de"] != "Disk 1: SMART-Prüfung bestanden" {
		t.Errorf("health = %+v, want passed", passed)
	}
	if temp := items[1]; temp.Unit != metrics.UnitCelsius || *temp.Value != 41 || !temp.History ||
		temp.Labels["es"] != "Samsung SSD 980 (nvme0): Temperatura" {
		t.Errorf("temperature = %+v, want 41 °C with history", temp)
	}
	if hours := items[4]; hours.Label != "Disk 1: Power-on hours" {
		t.Errorf("label = %q, want the disk without a model", hours.Label)
	}
	if clean := metrics.CleanExtras(got, 64); !reflect.DeepEqual(clean, got) {
		t.Errorf("CleanExtras() changed the extras: %+v", clean)
	}
}

func TestExtrasKeysEachDiskOnItsSerialNumber(t *testing.T) {
	values := func(disks []Disk) map[string]float64 {
		got := map[string]float64{}
		for _, item := range Extras(disks)[0].Items {
			got[item.ID] = *item.Value
		}
		return got
	}

	// sda and sdb go to each other's disk at the next start; the values stay
	// with the disks.
	before := values([]Disk{{Name: "sda", Serial: "WD-WX12D", Celsius: ptr(30)}, {Name: "sdb", Serial: "ZRT0ABCD", Celsius: ptr(40)}})
	after := values([]Disk{{Name: "sda", Serial: "ZRT0ABCD", Celsius: ptr(40)}, {Name: "sdb", Serial: "WD-WX12D", Celsius: ptr(30)}})
	want := map[string]float64{"wd-wx12d-temperature": 30, "zrt0abcd-temperature": 40}
	if !reflect.DeepEqual(before, want) || !reflect.DeepEqual(after, want) {
		t.Errorf("values = %v, then %v, want %v both times", before, after, want)
	}

	// Two disks that report the same serial number, and one without any, are
	// told by their names.
	got := values([]Disk{
		{Name: "sdc", Serial: "0000000000", Celsius: ptr(32)},
		{Name: "sdd", Serial: "0000000000", Celsius: ptr(33)},
		{Name: "sde", Serial: " - ", Celsius: ptr(34)},
	})
	want = map[string]float64{"0000000000-temperature": 32, "sdd-temperature": 33, "sde-temperature": 34}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("values = %v, want %v", got, want)
	}
}

func TestExtrasShowsADiskWithSMARTOff(t *testing.T) {
	got := Extras([]Disk{{Name: "sda", Model: "WDC", Serial: "WD-WX12D", SMARTOff: true}})

	if len(got) != 1 || len(got[0].Items) != 1 {
		t.Fatalf("Extras() = %+v, want one item", got)
	}
	item := got[0].Items[0]
	if item.ID != "wd-wx12d-health" || item.Unit != metrics.UnitText || item.Text != "–" || item.History ||
		item.Label != "WDC (sda): SMART off" || item.Labels["de"] != "WDC (sda): SMART aus" ||
		item.Labels["fr"] != "WDC (sda): SMART désactivé" || item.Labels["es"] != "WDC (sda): SMART desactivado" {
		t.Errorf("item = %+v, want SMART off in place of the check", item)
	}
}

func TestExtrasLogsOnceThatTheDisksReportTooManyValues(t *testing.T) {
	warnedTooMany.Store(false)
	t.Cleanup(func() { warnedTooMany.Store(false) })
	disk := func(n int) Disk {
		return Disk{Name: fmt.Sprintf("sd%c", 'a'+n), Passed: yes(), Celsius: ptr(30), PowerOnHours: ptr(1), ReallocatedSectors: ptr(0)}
	}
	var disks []Disk
	for n := range 16 {
		disks = append(disks, disk(n))
	}
	Extras(disks)
	if warnedTooMany.Load() {
		t.Error("16 disks of 4 values each were logged as too many")
	}
	Extras(append(disks, disk(16)))
	if !warnedTooMany.Load() {
		t.Error("17 disks of 4 values each were not logged as too many")
	}
}

func TestExtrasShowsADiskThatCannotBeRead(t *testing.T) {
	got := Extras([]Disk{{Name: "sda", Model: "WDC", Serial: "WD-WX12D", Unreadable: true}})

	if len(got) != 1 || len(got[0].Items) != 1 {
		t.Fatalf("Extras() = %+v, want one item", got)
	}
	item := got[0].Items[0]
	if item.ID != "wd-wx12d-health" || item.Unit != metrics.UnitText || item.Text != "✗" || item.History ||
		item.Label != "WDC (sda): Cannot be read" || item.Labels["de"] != "WDC (sda): Kann nicht gelesen werden" ||
		item.Labels["fr"] != "WDC (sda): Ne peut pas être lu" || item.Labels["es"] != "WDC (sda): No se puede leer" {
		t.Errorf("item = %+v, want that it cannot be read, in place of the check", item)
	}
}

func TestParseDiskPerformanceAddsReadsAndWrites(t *testing.T) {
	b := make([]byte, diskPerformanceSize)
	binary.LittleEndian.PutUint32(b[40:], 7)
	binary.LittleEndian.PutUint32(b[44:], 5)
	if got, ok := parseDiskPerformance(b); !ok || got != 12 {
		t.Errorf("parseDiskPerformance() = %d, %v, want 12", got, ok)
	}
	if _, ok := parseDiskPerformance(b[:40]); ok {
		t.Error("parseDiskPerformance() of a short answer is ok")
	}
}

func TestExtrasOfNoDisksIsNothing(t *testing.T) {
	if got := Extras(nil); got != nil {
		t.Errorf("Extras(nil) = %+v, want nil", got)
	}
	if got := Extras([]Disk{{Name: "sda"}}); got != nil {
		t.Errorf("Extras() of a disk without values = %+v, want nil", got)
	}
}

func TestIDOfKeepsRoomForTheLongestValue(t *testing.T) {
	a := idOf(strings.Repeat("disk", 20) + "1")
	b := idOf(strings.Repeat("disk", 20) + "2")
	if a == b || len(a+"-power-on-hours") > 40 || len(b+"-power-on-hours") > 40 {
		t.Errorf("idOf() = %q and %q, want two different ids that leave room for the longest value", a, b)
	}
	if got := idOf("WD-WCC7K0000000"); got != "wd-wcc7k0000000" {
		t.Errorf("idOf() = %q, want the serial number in lowercase", got)
	}
	for _, name := range []string{"", " ", "--"} {
		if got := idOf(name); got != "" {
			t.Errorf("idOf(%q) = %q, want nothing", name, got)
		}
	}
}

// fakeSource answers like the disks of a machine and counts the reads.
type fakeSource struct {
	mu     sync.Mutex
	asleep bool
	// failing makes sda fail to read, as a failing disk may.
	failing bool
	// gone are the disks no longer listed, by path.
	gone map[string]bool
	// ioCounts are the disks' counts of reads and writes, by path; a disk
	// without one is not counted.
	ioCounts map[string]uint64
	lists    int
	reads    int
}

func (f *fakeSource) list() ([]device, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lists++
	var devices []device
	for _, d := range []device{
		{name: "sda", path: "/dev/sda"},
		{name: "nvme0", path: "/dev/nvme0", nvme: true, model: "Samsung SSD 980", serial: "S64DNX0R1"},
		{name: "sdb", path: "/dev/sdb"},
	} {
		if !f.gone[d.path] {
			devices = append(devices, d)
		}
	}
	return devices, nil
}

func (f *fakeSource) read(d device) (Disk, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	switch {
	case d.path == "/dev/sdb":
		return Disk{}, errors.New("not passed through")
	case d.nvme:
		return Disk{Celsius: ptr(41)}, nil
	case f.asleep:
		return Disk{}, errAsleep
	case f.failing:
		return Disk{}, errors.New("input/output error")
	}
	return Disk{Model: "WDC", Serial: "WD-WX12D", Celsius: ptr(34)}, nil
}

func (f *fakeSource) ioCount(d device) (uint64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	count, ok := f.ioCounts[d.path]
	return count, ok
}

func (f *fakeSource) set(change func(f *fakeSource)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

func (f *fakeSource) counts() (lists, reads int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lists, f.reads
}

func TestReaderReadsEachDiskOncePerIntervalAndKeepsTheLastResult(t *testing.T) {
	src := &fakeSource{}
	r := newReader(src)
	start := time.Now()
	ctx := context.Background()

	r.refresh(ctx, start)
	got := r.Read(ctx, start.Add(5*time.Second))
	want := []Disk{
		{Name: "sda", Model: "WDC", Serial: "WD-WX12D", Celsius: ptr(34)},
		{Name: "nvme0", Model: "Samsung SSD 980", Serial: "S64DNX0R1", Celsius: ptr(41)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Read() = %+v, want %+v", got, want)
	}
	if lists, reads := src.counts(); lists != 1 || reads != 3 {
		t.Errorf("listed %d and read %d times before ReadInterval passed, want 1 and 3", lists, reads)
	}

	// A read interval later sda sleeps: it keeps its last result and the disks
	// are not listed again before an hour.
	src.mu.Lock()
	src.asleep = true
	src.mu.Unlock()
	r.refresh(ctx, start.Add(ReadInterval))
	got = r.Read(ctx, start.Add(ReadInterval+5*time.Second))
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Read() = %+v, want the sleeping disk's last result", got)
	}
	if lists, reads := src.counts(); lists != 1 || reads != 6 {
		t.Errorf("listed %d and read %d times, want 1 and 6", lists, reads)
	}
	if !r.warned["/dev/sdb"] {
		t.Error("the disk that cannot be read was not logged")
	}

	// Another read interval later sda cannot be read: it no longer shows the
	// check it passed before, but that it cannot be read.
	src.mu.Lock()
	src.asleep, src.failing = false, true
	src.mu.Unlock()
	r.refresh(ctx, start.Add(2*ReadInterval))
	got = r.Read(ctx, start.Add(2*ReadInterval+5*time.Second))
	unreadable := []Disk{{Name: "sda", Model: "WDC", Serial: "WD-WX12D", Unreadable: true}, want[1]}
	if !reflect.DeepEqual(got, unreadable) {
		t.Errorf("Read() = %+v, want %+v once sda fails", got, unreadable)
	}
	// It stays so while it cannot be read.
	r.refresh(ctx, start.Add(3*ReadInterval))
	if got := r.last(); !reflect.DeepEqual(got, unreadable) {
		t.Errorf("Read() = %+v, want %+v while sda fails", got, unreadable)
	}
}

func TestReaderDropsADiskThatWasUnpluggedAtOnce(t *testing.T) {
	src := &fakeSource{}
	r := newReader(src)
	start := time.Now()
	ctx := context.Background()
	r.refresh(ctx, start)

	// sdb, which was never read, does not make the disks be listed again.
	r.refresh(ctx, start.Add(ReadInterval))
	if lists, _ := src.counts(); lists != 1 {
		t.Errorf("listed %d times, want once while only sdb cannot be read", lists)
	}

	// sda is swapped out: its read fails, and listing the disks again drops
	// it instead of showing that it cannot be read.
	src.set(func(f *fakeSource) { f.failing, f.gone = true, map[string]bool{"/dev/sda": true} })
	r.refresh(ctx, start.Add(2*ReadInterval))
	if got := r.last(); len(got) != 1 || got[0].Name != "nvme0" {
		t.Errorf("last() = %+v, want only nvme0 once sda is gone", got)
	}
	if lists, _ := src.counts(); lists != 2 {
		t.Errorf("listed %d times, want again after sda failed", lists)
	}
}

func TestReaderLeavesADiskWithoutUseAlone(t *testing.T) {
	// The NVMe disk is read every time even without use.
	src := &fakeSource{ioCounts: map[string]uint64{"/dev/sda": 100, "/dev/nvme0": 7}}
	r := newReader(src)
	start := time.Now()
	ctx := context.Background()

	r.refresh(ctx, start)
	_, before := src.counts()

	// sda was not used since: it is not read again, and keeps its result.
	src.set(func(f *fakeSource) { f.failing = true })
	r.refresh(ctx, start.Add(ReadInterval))
	if _, reads := src.counts(); reads != before+2 {
		t.Errorf("read %d disks, want 2 without the unused sda", reads-before)
	}
	if got := r.last(); len(got) != 2 || got[0].Name != "sda" || got[0].Unreadable || got[0].Celsius == nil {
		t.Errorf("last() = %+v, want sda's last result", got)
	}

	// Once it was used, it is read again.
	src.set(func(f *fakeSource) { f.ioCounts["/dev/sda"] = 101 })
	r.refresh(ctx, start.Add(2*ReadInterval))
	if _, reads := src.counts(); reads != before+5 {
		t.Errorf("read %d disks, want 3 with the used sda", reads-before-2)
	}
	if got := r.last(); len(got) != 2 || !got[0].Unreadable {
		t.Errorf("last() = %+v, want sda that cannot be read", got)
	}

	// A disk that cannot be read is tried again even without use.
	r.refresh(ctx, start.Add(3*ReadInterval))
	if _, reads := src.counts(); reads != before+8 {
		t.Errorf("read %d disks, want 3 with the unreadable sda", reads-before-5)
	}

	// Once it is read again, it is left alone without use until its last
	// read is idleReadAfter old.
	src.set(func(f *fakeSource) { f.failing = false })
	r.refresh(ctx, start.Add(4*ReadInterval))
	r.refresh(ctx, start.Add(4*ReadInterval+idleReadAfter-time.Second))
	if _, reads := src.counts(); reads != before+13 {
		t.Errorf("read %d disks, want 3 and then 2 without the unused sda", reads-before-8)
	}
	r.refresh(ctx, start.Add(4*ReadInterval+idleReadAfter))
	if _, reads := src.counts(); reads != before+16 {
		t.Errorf("read %d disks, want 3 with sda read a day ago", reads-before-13)
	}
}

func TestReaderLogsADiskThatAlwaysSleeps(t *testing.T) {
	src := &fakeSource{asleep: true}
	r := newReader(src)
	start := time.Now()
	ctx := context.Background()

	r.refresh(ctx, start)
	r.refresh(ctx, start.Add(asleepLogAfter-time.Second))
	if r.asleepLogged["/dev/sda"] {
		t.Error("the sleeping disk was logged before asleepLogAfter")
	}
	r.refresh(ctx, start.Add(asleepLogAfter))
	if !r.asleepLogged["/dev/sda"] {
		t.Error("the disk that sleeps at every read was not logged")
	}

	// A read that fails in between ends the sleep, so it is counted anew.
	src = &fakeSource{asleep: true}
	r = newReader(src)
	r.refresh(ctx, start)
	src.set(func(f *fakeSource) { f.asleep, f.failing = false, true })
	r.refresh(ctx, start.Add(ReadInterval))
	src.set(func(f *fakeSource) { f.asleep = true })
	r.refresh(ctx, start.Add(asleepLogAfter))
	r.refresh(ctx, start.Add(asleepLogAfter+ReadInterval))
	if r.asleepLogged["/dev/sda"] {
		t.Error("a disk that failed in between was logged as asleep at every read")
	}
	r.refresh(ctx, start.Add(2*asleepLogAfter))
	if !r.asleepLogged["/dev/sda"] {
		t.Error("the disk asleep at every read since it failed was not logged")
	}

	// A disk that was read before is not logged however long it sleeps.
	src = &fakeSource{}
	r = newReader(src)
	r.refresh(ctx, start)
	src.set(func(f *fakeSource) { f.asleep = true })
	r.refresh(ctx, start.Add(ReadInterval))
	r.refresh(ctx, start.Add(ReadInterval+asleepLogAfter))
	if r.asleepLogged["/dev/sda"] {
		t.Error("a disk read before was logged as always asleep")
	}
}

func TestReaderHoldsTheReadInterval(t *testing.T) {
	if ReadInterval <= 20*time.Minute {
		t.Errorf("ReadInterval = %v, want more than 20 minutes, so disks can switch off", ReadInterval)
	}
	src := &fakeSource{}
	r := newReader(src)
	start := time.Now()
	ctx := context.Background()
	r.refresh(ctx, start)
	reading := func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.reading
	}

	r.Read(ctx, start.Add(ReadInterval-time.Second))
	if reading() {
		t.Error("Read() one second before ReadInterval started a read")
	}
	r.Read(ctx, start.Add(ReadInterval))
	if !reading() {
		t.Error("Read() at ReadInterval did not start a read")
	}
	deadline := time.Now().Add(5 * time.Second)
	for reading() {
		if time.Now().After(deadline) {
			t.Fatal("the background read did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestReaderStartsAReadInTheBackground(t *testing.T) {
	src := &fakeSource{}
	r := newReader(src)
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
	if lists, reads := src.counts(); lists != 1 || reads != 3 {
		t.Errorf("listed %d and read %d times, want 1 and 3", lists, reads)
	}
}

func TestNilReaderReadsNothing(t *testing.T) {
	var r *Reader
	if got := r.Read(context.Background(), time.Now()); got != nil {
		t.Errorf("Read() = %+v, want nil", got)
	}
}
