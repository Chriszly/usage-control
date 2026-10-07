// Package kernel reads what the operating system's kernel is busy with, for
// the kernel add-on.
//
// On Linux it reads context switches, interrupts and new processes per second from
// /proc/stat, the open files from /proc/sys/fs/file-nr, the sockets in use
// from /proc/net/sockstat and the established TCP connections and the TCP
// retransmissions per second from /proc/net/snmp.
//
// /proc/net shows the network namespace of the process that reads it, so the
// add-on reads /proc/1/net: the host's init's, which is the host's network
// both under systemd with PrivateNetwork and in a container that mounts the
// host's /proc.
//
// Windows has no /proc; system_windows.go reads the closest values Windows
// itself offers (see there). Other systems get no values.
//
// It only reads; nothing in here changes the machine.
package kernel

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// counters are the kernel's running totals, of which the add-on shows the
// change per second. A total the kernel did not report is left out.
type counters map[string]uint64

// The values of the group, in the order they are shown.
const (
	contextSwitches = "context-switches"
	interrupts      = "interrupts"
	newProcesses    = "new-processes"
	openFiles       = "open-files"
	handles         = "handles"
	sockets         = "sockets"
	tcpEstablished  = "tcp-established"
	retransmissions = "tcp-retransmissions"
)

var order = []string{
	contextSwitches, interrupts, newProcesses, openFiles, handles,
	sockets, tcpEstablished, retransmissions,
}

// rates are the values shown per second, from the change of a counter.
var rates = map[string]bool{contextSwitches: true, interrupts: true, newProcesses: true, retransmissions: true}

var labels = map[string]struct {
	en    string
	other map[string]string
}{
	contextSwitches: {"Context switches", map[string]string{"de": "Kontextwechsel", "fr": "Changements de contexte", "es": "Cambios de contexto"}},
	interrupts:      {"Interrupts", map[string]string{"de": "Interrupts", "fr": "Interruptions", "es": "Interrupciones"}},
	newProcesses:    {"New processes", map[string]string{"de": "Neue Prozesse", "fr": "Nouveaux processus", "es": "Procesos nuevos"}},
	openFiles:       {"Open files", map[string]string{"de": "Offene Dateien", "fr": "Fichiers ouverts", "es": "Archivos abiertos"}},
	handles:         {"Open handles", map[string]string{"de": "Offene Handles", "fr": "Handles ouverts", "es": "Handles abiertos"}},
	sockets:         {"Sockets in use", map[string]string{"de": "Belegte Sockets", "fr": "Sockets utilisés", "es": "Sockets en uso"}},
	tcpEstablished:  {"Established TCP connections", map[string]string{"de": "Aufgebaute TCP-Verbindungen", "fr": "Connexions TCP établies", "es": "Conexiones TCP establecidas"}},
	retransmissions: {"TCP retransmissions", map[string]string{"de": "TCP-Neuübertragungen", "fr": "Retransmissions TCP", "es": "Retransmisiones TCP"}},
}

// Source is what the add-on reads: the values by id at a time.
type Source interface {
	Read(now time.Time) map[string]float64
}

// meter turns counters into values, the rates from the change since the
// previous call, so the first call leaves them out.
type meter struct {
	previous counters
	at       time.Time
}

func (m *meter) measure(now time.Time, maps ...counters) map[string]float64 {
	values := map[string]float64{}
	totals := counters{}
	for _, found := range maps {
		for id, n := range found {
			if !rates[id] {
				values[id] = float64(n)
				continue
			}
			totals[id] = n
			before, ok := m.previous[id]
			if elapsed := now.Sub(m.at).Seconds(); ok && elapsed > 0 && n >= before {
				values[id] = float64(n-before) / elapsed
			}
		}
	}
	m.previous, m.at = totals, now
	return values
}

// Reader reads the Linux kernel's values from /proc and keeps the counters
// of the previous read for the rates.
type Reader struct {
	proc  string
	meter meter
}

// NewReader returns a Reader for the machine. procDir is where /proc is,
// which in a container is where the host's /proc is mounted.
func NewReader(procDir string) *Reader {
	return &Reader{proc: procDir}
}

// Read returns the values by id. Rates are the average since the previous
// call, so the first call leaves them out.
func (r *Reader) Read(now time.Time) map[string]float64 {
	return r.meter.measure(now,
		parseStat(readText(filepath.Join(r.proc, "stat"))),
		parseFileNr(readText(filepath.Join(r.proc, "sys", "fs", "file-nr"))),
		parseSockstat(readText(filepath.Join(r.proc, "1", "net", "sockstat"))),
		parseSNMP(readText(filepath.Join(r.proc, "1", "net", "snmp"))),
	)
}

// Extras returns the values as the group of extras the collector shows.
func Extras(values map[string]float64) []metrics.Extra {
	if len(values) == 0 {
		return nil
	}
	group := metrics.Extra{
		ID:     "kernel",
		Title:  "Kernel",
		Titles: map[string]string{"de": "Kernel", "fr": "Noyau", "es": "Núcleo"},
	}
	for _, id := range order {
		value, ok := values[id]
		if !ok {
			continue
		}
		unit := metrics.UnitNumber
		if rates[id] {
			unit = metrics.UnitPerSecond
		}
		group.Items = append(group.Items, metrics.ExtraItem{
			ID:      id,
			Label:   labels[id].en,
			Labels:  labels[id].other,
			Unit:    unit,
			Value:   &value,
			History: true,
		})
	}
	return []metrics.Extra{group}
}

// HostProc returns where /proc is: HOST_PROC in a container that mounts the
// host's /proc there, else /proc.
func HostProc() string {
	if dir := os.Getenv("HOST_PROC"); dir != "" {
		return dir
	}
	return "/proc"
}

// readText reads a file under /proc, or returns "" when it cannot be read.
// Its path is the /proc folder from the settings and names the kernel gives
// its files.
func readText(path string) string {
	text, err := os.ReadFile(path) //nolint:gosec // see above
	if err != nil {
		return ""
	}
	return string(text)
}

// parseStat reads the lines "ctxt", "intr" (whose first number is the total)
// and "processes" of /proc/stat.
func parseStat(text string) counters {
	ids := map[string]string{"ctxt": contextSwitches, "intr": interrupts, "processes": newProcesses}
	found := counters{}
	scanner := bufio.NewScanner(strings.NewReader(text))
	// The intr line has a number per interrupt and can be long.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		if id, ok := ids[fields[0]]; ok {
			if n, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
				found[id] = n
			}
		}
	}
	return found
}

// parseFileNr reads the first number of /proc/sys/fs/file-nr: the open
// files of the whole machine.
func parseFileNr(text string) counters {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return nil
	}
	n, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return nil
	}
	return counters{openFiles: n}
}

// parseSockstat reads "sockets: used N" of /proc/net/sockstat. Its
// "TCP: inuse" is left out: it counts listening sockets too, so it is not
// the connections Windows counts (see parseSNMP).
func parseSockstat(text string) counters {
	for line := range strings.Lines(text) {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != "sockets:" || fields[1] != "used" {
			continue
		}
		if n, err := strconv.ParseUint(fields[2], 10, 64); err == nil {
			return counters{sockets: n}
		}
	}
	return nil
}

// parseSNMP reads CurrEstab and RetransSegs of /proc/net/snmp, where each
// protocol has a line of names followed by a line of numbers. CurrEstab is
// the TCP connections in the states ESTABLISHED and CLOSE-WAIT, as Windows
// counts them, over IPv4 and IPv6.
func parseSNMP(text string) counters {
	ids := map[string]string{"CurrEstab": tcpEstablished, "RetransSegs": retransmissions}
	var names []string
	for line := range strings.Lines(text) {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "Tcp:" {
			continue
		}
		if names == nil {
			names = fields
			continue
		}
		found := counters{}
		for i, name := range names {
			if id, ok := ids[name]; ok && i < len(fields) {
				if n, err := strconv.ParseUint(fields[i], 10, 64); err == nil {
					found[id] = n
				}
			}
		}
		if len(found) == 0 {
			return nil
		}
		return found
	}
	return nil
}
