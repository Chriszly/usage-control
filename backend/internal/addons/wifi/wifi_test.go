package wifi

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
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
		{Interface: "wlan0", Key: "wlan0", QualityPercent: 80, SignalDBm: -54, HasSignal: true},
		{Interface: "wlp2s0", Key: "wlp2s0", QualityPercent: 80},
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
		if item.Value == nil {
			t.Errorf("item %s has no value", item.ID)
		}
		// Only the quality is charted: the signal in dBm is below 0, where
		// a chart has no room.
		if wantHistory := strings.HasSuffix(item.ID, "-quality"); item.History != wantHistory {
			t.Errorf("item %s keeps history %v, want %v", item.ID, item.History, wantHistory)
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

// The Windows laptop's adapter: its description takes 78 characters.
const longName = "Qualcomm FastConnect 7800 Wi-Fi 7 High Band Simultaneous (HBS) Network Adapter"

func TestLabelsKeepWhatTellsThemApart(t *testing.T) {
	extras := Extras([]Reading{{Interface: longName, Key: "00112233445566778899aabbccddeeff", QualityPercent: 76, SignalDBm: -62, HasSignal: true}})

	// As a hub stores them, with every label cut to its most characters.
	got := metrics.CleanExtras(extras, 64)

	if len(got) != 1 || len(got[0].Items) != 2 {
		t.Fatalf("CleanExtras(Extras()) = %+v, want the quality and the signal", got)
	}
	quality, signal := got[0].Items[0], got[0].Items[1]
	ends := map[string][2]string{
		"":   {" link quality", " signal (dBm)"},
		"de": {" Verbindungsqualität", " Signal (dBm)"},
		"fr": {" qualité du lien", " signal (dBm)"},
		"es": {" calidad del enlace", " señal (dBm)"},
	}
	for lang, end := range ends {
		q, s := quality.Label, signal.Label
		if lang != "" {
			q, s = quality.Labels[lang], signal.Labels[lang]
		}
		for i, l := range []string{q, s} {
			if !strings.HasPrefix(l, "Qualcomm FastConnect 7800") || !strings.HasSuffix(l, end[i]) || utf8.RuneCountInString(l) > 80 {
				t.Errorf("label in %q = %q, want the adapter's name cut to end in %q within 80 characters", lang, l, end[i])
			}
		}
	}
	if quality.Label != "Qualcomm FastConnect 7800 Wi-Fi 7 High Band Simultaneous (HBS) Net… link quality" ||
		signal.Labels["de"] != "Qualcomm FastConnect 7800 Wi-Fi 7 High Band Simultaneous (HBS) Net… Signal (dBm)" {
		t.Errorf("labels = %q, %q", quality.Label, signal.Labels["de"])
	}
	if got := label("wlan0", "link quality"); got != "wlan0 link quality" {
		t.Errorf("label() of a short name = %q, want it whole", got)
	}
}

func TestLabelsOfASecondAdapterOfTheSameModelStayApart(t *testing.T) {
	first, second := label(longName, "Verbindungsqualität"), label(longName+" #2", "Verbindungsqualität")
	if first == second {
		t.Errorf("labels of two adapters of the same model are both %q, want them apart", first)
	}
	if want := "Qualcomm FastConnect 7800 Wi-Fi 7 High Band Simultaneous… #2 Verbindungsqualität"; second != want {
		t.Errorf("label() of the second adapter = %q, want %q", second, want)
	}
	if n := utf8.RuneCountInString(second); n > maxLabel {
		t.Errorf("label() of the second adapter takes %d characters, more than %d", n, maxLabel)
	}
}

func TestAdaptersOfTheSameModelStayApart(t *testing.T) {
	extras := Extras([]Reading{
		{Interface: "Intel(R) Wi-Fi 6E AX211 160MHz", Key: "00112233445566778899aabbccddeeff", QualityPercent: 80},
		{Interface: "Intel(R) Wi-Fi 6E AX211 160MHz", Key: "8899aabbccddeeff0011223344556677", QualityPercent: 40},
	})

	got := metrics.CleanExtras(extras, 64)

	var ids []string
	for _, item := range got[0].Items {
		ids = append(ids, item.ID)
	}
	want := []string{"00112233445566778899aabbccddeeff-quality", "8899aabbccddeeff0011223344556677-quality"}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("ids = %q, want %q", ids, want)
	}
}

func TestIDKeepsItsSuffix(t *testing.T) {
	if got := idOf("wlp2s0", "-signal"); got != "wlp2s0-signal" {
		t.Errorf("idOf() of a Linux name = %q, want wlp2s0-signal", got)
	}
	// Longer keys than there are are cut, the same for both suffixes, and
	// stay apart by their checksum.
	quality, signal := idOf(longName, "-quality"), idOf(longName, "-signal")
	if quality != "qualcomm-fastconnect-78-3c9bcfc0-quality" || signal != "qualcomm-fastconnect-78-3c9bcfc0-signal" {
		t.Errorf("ids = %q, %q", quality, signal)
	}
	if other := idOf(longName+" #2", "-quality"); other == quality || len(other) > 40 {
		t.Errorf("ids of two long keys = %q, %q, want them apart", quality, other)
	}
	if got := idOf("Ω", "-quality"); got != "wifi-quality" {
		t.Errorf("idOf() of a key without letters = %q, want wifi-quality", got)
	}
}
