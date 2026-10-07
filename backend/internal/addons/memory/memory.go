// Package memory reads details of the machine's memory beyond what
// usage-control itself shows, for the memory add-on. On Linux it reads from
// /proc/meminfo how much is dirty, being written back, held by the kernel's
// slab caches, shared, used for page tables and committed, and from
// /proc/vmstat the page faults and the swapping per second. On Windows it
// reads the nearest performance counters of the Memory object (see
// counters.go); elsewhere it reports nothing.
//
// It only reads; nothing in here changes the machine.
package memory

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/addons"
	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// size is a value of /proc/meminfo shown in bytes.
type size struct {
	key    string // the name in /proc/meminfo
	id     string
	label  string
	labels map[string]string
}

var sizes = []size{
	{"Dirty", "dirty", "Dirty (waiting to be written)", map[string]string{"de": "Ungeschrieben (Dirty)", "fr": "Modifiée, pas encore écrite (Dirty)", "es": "Modificada, sin escribir (Dirty)"}},
	{"Writeback", "writeback", "Being written back", map[string]string{"de": "Wird geschrieben (Writeback)", "fr": "En cours d'écriture (Writeback)", "es": "Escribiéndose (Writeback)"}},
	{"Slab", "slab", "Kernel caches (slab)", map[string]string{"de": "Kernel-Caches (Slab)", "fr": "Caches du noyau (slab)", "es": "Cachés del núcleo (slab)"}},
	{"Shmem", "shared", "Shared memory", map[string]string{"de": "Gemeinsamer Speicher", "fr": "Mémoire partagée", "es": "Memoria compartida"}},
	{"PageTables", "page-tables", "Page tables", map[string]string{"de": "Seitentabellen", "fr": "Tables de pages", "es": "Tablas de páginas"}},
	committed,
}

// committed is the memory the system has promised to programs, which Windows
// counts too.
var committed = size{"Committed_AS", "committed", "Committed", map[string]string{"de": "Zugesagt (Committed)", "fr": "Engagée (Committed)", "es": "Comprometida (Committed)"}}

// rate is a counter of /proc/vmstat shown per second. A counter of pages
// swapped is shown in bytes per second, the others as events per second.
type rate struct {
	key    string // the name in /proc/vmstat
	id     string
	label  string
	labels map[string]string
	pages  bool
}

var rates = []rate{
	pageFaults,
	{"pgmajfault", "major-page-faults", "Major page faults (read from disk)", map[string]string{"de": "Schwere Seitenfehler (von der Platte)", "fr": "Défauts de page majeurs (lus sur disque)", "es": "Fallos de página mayores (leídos del disco)"}, false},
	{"pswpin", "swap-in", "Swapped in", map[string]string{"de": "Aus dem Swap gelesen", "fr": "Lu depuis le swap", "es": "Leído del swap"}, true},
	{"pswpout", "swap-out", "Swapped out", map[string]string{"de": "In den Swap geschrieben", "fr": "Écrit dans le swap", "es": "Escrito en el swap"}, true},
}

// pageFaults are all page faults, those resolved in memory and those read from
// disk, which Windows counts too.
var pageFaults = rate{"pgfault", "page-faults", "Page faults", map[string]string{"de": "Seitenfehler", "fr": "Défauts de page", "es": "Fallos de página"}, false}

// Reader reads the memory details of the machine.
type Reader struct {
	meminfo  string
	vmstat   string
	pageSize float64
	// The counters of /proc/vmstat at the previous read, and when it was.
	previous     map[string]uint64
	previousTime time.Time
}

// NewReader returns a Reader for the machine. procDir is where /proc is,
// which in a container is where the host's /proc is mounted.
func NewReader(procDir string) *Reader {
	return &Reader{
		meminfo:  filepath.Join(procDir, "meminfo"),
		vmstat:   filepath.Join(procDir, "vmstat"),
		pageSize: float64(os.Getpagesize()),
	}
}

// Read returns the memory details as the group of extras the collector
// shows, or nothing where /proc is missing. The rates are the average since
// the previous call, so the first call leaves them out.
func (r *Reader) Read(now time.Time) []metrics.Extra {
	var items []metrics.ExtraItem
	if values := readKeys(r.meminfo); values != nil {
		for _, s := range sizes {
			kilobytes, ok := values[s.key]
			if !ok {
				continue
			}
			bytes := float64(kilobytes) * 1024
			items = append(items, metrics.ExtraItem{
				ID: s.id, Label: s.label, Labels: s.labels, Unit: metrics.UnitBytes, Value: &bytes, History: true,
			})
		}
	}
	counters := readKeys(r.vmstat)
	if seconds := now.Sub(r.previousTime).Seconds(); r.previous != nil && counters != nil && seconds > 0 {
		for _, c := range rates {
			value, ok := counters[c.key]
			before, okBefore := r.previous[c.key]
			if !ok || !okBefore || value < before {
				continue
			}
			perSecond := float64(value-before) / seconds
			unit := metrics.UnitPerSecond
			if c.pages {
				perSecond *= r.pageSize
				unit = metrics.UnitBytesPerSecond
			}
			items = append(items, metrics.ExtraItem{
				ID: c.id, Label: c.label, Labels: c.labels, Unit: unit, Value: &perSecond, History: true,
			})
		}
	}
	r.previous, r.previousTime = counters, now
	return grouped(items)
}

// groups are the groups of extras the values are shown in, in their order,
// each with values of a similar size: a chart is drawn per group and unit,
// and a few kilobytes of dirty memory or a few major page faults would be a
// flat line next to gigabytes committed or thousands of page faults. A value
// whose id no group lists is left out.
var groups = []struct {
	id, title string
	titles    map[string]string
	ids       []string
}{
	{
		"memory", "Memory details",
		map[string]string{"de": "Speicherdetails", "fr": "Détails de la mémoire", "es": "Detalles de la memoria"},
		[]string{"slab", "shared", "page-tables", "pool-paged", "pool-nonpaged"},
	},
	{
		"memory-committed", "Memory: committed",
		map[string]string{"de": "Speicher: zugesagt", "fr": "Mémoire : engagée", "es": "Memoria: comprometida"},
		[]string{committed.id},
	},
	{
		"memory-writeback", "Memory: waiting to be written",
		map[string]string{"de": "Speicher: ungeschrieben", "fr": "Mémoire : pas encore écrite", "es": "Memoria: sin escribir"},
		[]string{"dirty", "writeback", "modified"},
	},
	{
		"memory-faults", "Memory: page faults",
		map[string]string{"de": "Speicher: Seitenfehler", "fr": "Mémoire : défauts de page", "es": "Memoria: fallos de página"},
		[]string{pageFaults.id},
	},
	{
		"memory-disk", "Memory: disk access",
		map[string]string{"de": "Speicher: Plattenzugriffe", "fr": "Mémoire : accès disque", "es": "Memoria: accesos al disco"},
		[]string{"major-page-faults", "page-reads", "swap-in", "swap-out", "paged-in", "paged-out"},
	},
}

// grouped returns the groups of extras holding items, in their order,
// leaving out groups without items.
func grouped(items []metrics.ExtraItem) []metrics.Extra {
	var extras []metrics.Extra
	for _, g := range groups {
		group := metrics.Extra{ID: g.id, Title: g.title, Titles: g.titles}
		for _, item := range items {
			if slices.Contains(g.ids, item.ID) {
				group.Items = append(group.Items, item)
			}
		}
		if len(group.Items) > 0 {
			extras = append(extras, group)
		}
	}
	return extras
}

// readKeys reads a file of /proc whose lines are a name and a whole number,
// such as /proc/meminfo ("Dirty:  1234 kB") or /proc/vmstat ("pgfault 5678").
// It returns nil when the file cannot be read, and logs once a failure other
// than a missing file.
func readKeys(file string) map[string]uint64 {
	f, err := os.Open(file) //nolint:gosec // a fixed file below the /proc the HOST_PROC setting names
	if err != nil {
		addons.WarnRead(file, err)
		return nil
	}
	defer func() { _ = f.Close() }()
	return parseKeys(f)
}

// parseKeys reads the lines of a /proc file such as /proc/meminfo or
// /proc/vmstat into their numbers by name, leaving out lines it cannot read.
func parseKeys(f io.Reader) map[string]uint64 {
	values := map[string]uint64{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		n, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		values[strings.TrimSuffix(fields[0], ":")] = n
	}
	return values
}

// HostProc returns where /proc is: HOST_PROC in a container that mounts the
// host's /proc there, else /proc.
func HostProc() string {
	if dir := os.Getenv("HOST_PROC"); dir != "" {
		return dir
	}
	return "/proc"
}
