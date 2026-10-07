// Package pressure reads Linux pressure stall information (PSI), for the
// pressure add-on: the share of the last ten seconds in which tasks had to
// wait for the CPU, for memory or for disk and other I/O, from
// /proc/pressure. Kernels built without PSI have no such files, and nothing
// is read there. Windows has no such measurement; there the add-on reports the
// closest signals its performance counters offer (see counters.go), and other
// systems report nothing.
//
// It only reads; nothing in here changes the machine.
package pressure

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// value is one value the add-on reports: the avg10 of a line of a file in
// /proc/pressure.
type value struct {
	file, line string
	id, label  string
	labels     map[string]string
}

// values are what the add-on reports, in this order. "some" is the share of
// time at least one task waited, "full" the share in which every task that
// could run waited at once.
var values = []value{
	{"cpu", "some", "cpu-some", "CPU: tasks waiting", map[string]string{
		"de": "CPU: Prozesse warten", "fr": "Processeur : tâches en attente", "es": "CPU: tareas en espera",
	}},
	{"memory", "some", "memory-some", "Memory: tasks waiting", map[string]string{
		"de": "Arbeitsspeicher: Prozesse warten", "fr": "Mémoire : tâches en attente", "es": "Memoria: tareas en espera",
	}},
	{"memory", "full", "memory-full", "Memory: all tasks stalled", map[string]string{
		"de": "Arbeitsspeicher: alle Prozesse blockiert", "fr": "Mémoire : toutes les tâches bloquées", "es": "Memoria: todas las tareas bloqueadas",
	}},
	{"io", "some", "io-some", "Disks and I/O: tasks waiting", map[string]string{
		"de": "Datenträger und E/A: Prozesse warten", "fr": "Disques et E/S : tâches en attente", "es": "Discos y E/S: tareas en espera",
	}},
	{"io", "full", "io-full", "Disks and I/O: all tasks stalled", map[string]string{
		"de": "Datenträger und E/A: alle Prozesse blockiert", "fr": "Disques et E/S : toutes les tâches bloquées", "es": "Discos y E/S: todas las tareas bloqueadas",
	}},
}

// Read returns the pressure the kernel reports under procDir, which in a
// container is where the host's /proc is mounted, as the group of extras the
// collector shows. It returns nothing when the kernel reports none.
func Read(procDir string) []metrics.Extra {
	files := map[string]map[string]float64{}
	g := group()
	for _, v := range values {
		lines, read := files[v.file]
		if !read {
			lines = readFile(filepath.Join(procDir, "pressure", v.file))
			files[v.file] = lines
		}
		percent, ok := lines[v.line]
		if !ok {
			continue
		}
		g.Items = append(g.Items, metrics.ExtraItem{
			ID:      v.id,
			Label:   v.label,
			Labels:  v.labels,
			Unit:    metrics.UnitPercent,
			Value:   &percent,
			History: true,
		})
	}
	if len(g.Items) == 0 {
		return nil
	}
	return []metrics.Extra{g}
}

// readFile reads a file of /proc/pressure, or nothing when it does not exist.
func readFile(path string) map[string]float64 {
	text, err := os.ReadFile(path) //nolint:gosec // a fixed file below the /proc that HOST_PROC names
	if err != nil {
		return nil
	}
	return parse(string(text))
}

// parse reads the avg10 of each line of a pressure file, such as
// "some avg10=0.38 avg60=0.18 avg300=0.11 total=1650969", by the line's
// first word.
func parse(text string) map[string]float64 {
	avg10 := map[string]float64{}
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		for _, field := range fields[1:] {
			number, found := strings.CutPrefix(field, "avg10=")
			if !found {
				continue
			}
			if percent, err := strconv.ParseFloat(number, 64); err == nil && percent >= 0 && percent <= 100 {
				avg10[fields[0]] = percent
			}
		}
	}
	return avg10
}

// HostProc returns where /proc is: HOST_PROC in a container that mounts the
// host's /proc there, else /proc.
func HostProc() string {
	if dir := os.Getenv("HOST_PROC"); dir != "" {
		return dir
	}
	return "/proc"
}
