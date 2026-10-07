package kernel

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

const sockstat = `sockets: used 56
TCP: inuse 34 orphan 0 tw 3 alloc 40 mem 2
UDP: inuse 4 mem 1
`

func snmp(retrans string) string {
	return `Ip: Forwarding DefaultTTL InReceives
Ip: 1 64 12345
Tcp: RtoAlgorithm RtoMin RtoMax MaxConn ActiveOpens PassiveOpens AttemptFails EstabResets CurrEstab InSegs OutSegs RetransSegs InErrs OutRsts InCsumErrors
Tcp: 1 200 120000 -1 70 28 0 21 30 12761 23403 ` + retrans + ` 0 26 0
Udp: InDatagrams NoPorts
Udp: 5 0
`
}

func stat(ctxt, intr, processes string) string {
	return "cpu  1 2 3 4\ncpu0 1 2 3 4\nintr " + intr + " 0 7 0 12\nctxt " + ctxt + "\nbtime 1700000000\nprocesses " + processes + "\nprocs_running 2\n"
}

func TestReadMeasuresRatesSinceThePreviousRead(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"stat":            stat("1000", "500", "100"),
		"sys/fs/file-nr":  "1888\t0\t9223372036854775807\n",
		"1/net/sockstat":  sockstat,
		"1/net/snmp":      snmp("10"),
		"self/net/snmp":   snmp("999999"), // another network namespace's, not read
		"1/net/sockstat6": "TCP6: inuse 3\n",
	})
	r := NewReader(dir)
	start := time.Now()

	first := r.Read(start)
	// Established connections, from CurrEstab, not the 34 TCP sockets in use,
	// which count listening sockets too.
	want := map[string]float64{openFiles: 1888, sockets: 56, tcpEstablished: 30}
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("first Read() = %v, want %v without rates yet", first, want)
	}

	writeFiles(t, dir, map[string]string{
		"stat":       stat("11000", "2500", "110"),
		"1/net/snmp": snmp("14"),
	})
	got := r.Read(start.Add(2 * time.Second))
	want = map[string]float64{
		contextSwitches: 5000, interrupts: 1000, newProcesses: 5, retransmissions: 2,
		openFiles: 1888, sockets: 56, tcpEstablished: 30,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("second Read() = %v, want %v", got, want)
	}
}

func TestReadWithoutProcGivesNothing(t *testing.T) {
	r := NewReader(t.TempDir())
	r.Read(time.Now())
	if got := r.Read(time.Now().Add(5 * time.Second)); len(got) != 0 {
		t.Errorf("Read() = %v, want nothing", got)
	}
	if got := Extras(nil); got != nil {
		t.Errorf("Extras(nil) = %v, want nil", got)
	}
}

func TestACounterThatGoesBackIsLeftOut(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"stat": stat("1000", "500", "100")})
	r := NewReader(dir)
	start := time.Now()
	r.Read(start)
	writeFiles(t, dir, map[string]string{"stat": stat("900", "600", "100")})

	got := r.Read(start.Add(time.Second))

	want := map[string]float64{interrupts: 100, newProcesses: 0}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Read() = %v, want %v", got, want)
	}
}

func TestParsersSkipWhatTheyDoNotKnow(t *testing.T) {
	if got := parseFileNr("garbage"); got != nil {
		t.Errorf("parseFileNr(garbage) = %v, want nil", got)
	}
	if got := parseSNMP("Tcp: RtoAlgorithm\nTcp: 1\n"); got != nil {
		t.Errorf("parseSNMP() without CurrEstab and RetransSegs = %v, want nil", got)
	}
	if got := parseSNMP("Tcp: CurrEstab InSegs\nTcp: 12 400\n"); !reflect.DeepEqual(got, counters{tcpEstablished: 12}) {
		t.Errorf("parseSNMP() = %v, want only 12 established connections", got)
	}
	if got := parseSockstat("TCP: inuse 7 orphan 0\nsockets: used 9\n"); !reflect.DeepEqual(got, counters{sockets: 9}) {
		t.Errorf("parseSockstat() = %v, want only 9 sockets", got)
	}
	if got := parseSockstat("TCP: inuse 7\n"); got != nil {
		t.Errorf("parseSockstat() without sockets = %v, want nil", got)
	}
}

func TestExtrasKeepsTheOrderAndUnits(t *testing.T) {
	got := Extras(map[string]float64{retransmissions: 0.5, openFiles: 1888, contextSwitches: 4200})

	if len(got) != 1 || got[0].ID != "kernel" || got[0].Title != "Kernel" || got[0].Titles["de"] != "Kernel" {
		t.Fatalf("Extras() = %+v, want the kernel group", got)
	}
	var ids, units []string
	for _, item := range got[0].Items {
		ids = append(ids, item.ID)
		units = append(units, string(item.Unit))
		if !item.History || item.Labels["fr"] == "" || item.Value == nil {
			t.Errorf("item %+v wants a value, history and translations", item)
		}
	}
	if want := []string{contextSwitches, openFiles, retransmissions}; !reflect.DeepEqual(ids, want) {
		t.Errorf("ids = %v, want %v", ids, want)
	}
	if want := []string{"perSecond", "number", "perSecond"}; !reflect.DeepEqual(units, want) {
		t.Errorf("units = %v, want %v", units, want)
	}
}
