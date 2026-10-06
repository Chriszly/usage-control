package wifi

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const sample = `Inter-| sta-|   Quality        |   Discarded packets               | Missed | WE
 face | tus | link level noise |  nwid  crypt   frag  retry   misc | beacon | 22
 wlan0: 0000   56.  -54.  -256        0      0      0      0     12        0
wlp2s0: 0000   80    80     0         0      0      0      0      0        0
  wlx1: 0000    0     0     0         0      0      0      0      0        0
`

func TestParseReadsQualityAndSignal(t *testing.T) {
	got := parse(sample)

	want := []Reading{
		{Interface: "wlan0", QualityPercent: 80, SignalDBm: -54, HasSignal: true},
		{Interface: "wlp2s0", QualityPercent: 80},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parse() = %+v, want %+v", got, want)
	}
}

func TestReadWithoutWirelessReportsNothing(t *testing.T) {
	dir := t.TempDir()
	if got := Read(File(dir)); got != nil {
		t.Errorf("Read() without the file = %+v, want nothing", got)
	}
	file := File(dir)
	if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
		t.Fatal(err)
	}
	header := "Inter-| sta-|   Quality        |   Discarded packets               | Missed | WE\n" +
		" face | tus | link level noise |  nwid  crypt   frag  retry   misc | beacon | 22\n"
	if err := os.WriteFile(file, []byte(header), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Extras(Read(file)); got != nil {
		t.Errorf("Extras() without interfaces = %+v, want nothing", got)
	}
}

func TestExtrasDescribeEachInterface(t *testing.T) {
	got := Extras(parse(sample))

	if len(got) != 1 || got[0].ID != "wifi" || got[0].Titles["de"] != "WLAN" {
		t.Fatalf("Extras() = %+v, want one Wi-Fi group", got)
	}
	var ids []string
	for _, item := range got[0].Items {
		ids = append(ids, item.ID+" "+string(item.Unit)+" "+item.Label)
		if !item.History || item.Value == nil {
			t.Errorf("item %s keeps no history or has no value", item.ID)
		}
	}
	want := []string{
		"wlan0-quality percent wlan0 link quality",
		"wlan0-signal number wlan0 signal (dBm)",
		"wlp2s0-quality percent wlp2s0 link quality",
	}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("items = %q, want %q", ids, want)
	}
	if *got[0].Items[1].Value != -54 {
		t.Errorf("signal = %v, want -54", *got[0].Items[1].Value)
	}
}
