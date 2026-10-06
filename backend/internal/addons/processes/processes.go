// Package processes reads the busiest processes, for the processes add-on:
// the ten that used the most CPU since the previous read and the ten that
// hold the most memory. On Linux it reads /proc/<pid>/stat, one small file
// per process; on Windows it asks the system for every process at once.
//
// It only reads; nothing in here changes the machine.
package processes

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// Top is how many processes each group lists.
const Top = 10

// Process is one running process.
type Process struct {
	PID int
	// Start tells a process apart from an earlier one with the same PID.
	Start uint64
	Name  string
	// CPU is the CPU time the process used since it started, in the unit of
	// Sample.Total.
	CPU uint64
	// Memory is the memory the process holds in RAM (resident set, or the
	// working set on Windows), in bytes.
	Memory uint64
}

// Sample is every process at one moment.
type Sample struct {
	Processes []Process
	// Total is the CPU time of the whole machine, all its CPUs together, in
	// the same unit as Process.CPU, counted from any fixed point.
	Total uint64
}

// Source takes a Sample, or is false when the machine offers none.
type Source func(now time.Time) (Sample, bool)

type key struct {
	pid   int
	start uint64
}

// Reader reads the busiest processes from a Source.
type Reader struct {
	source    Source
	previous  map[key]uint64
	lastTotal uint64
}

// NewReader returns a Reader that takes its samples from source.
func NewReader(source Source) *Reader {
	return &Reader{source: source}
}

// Read returns the busiest processes as extras: by CPU, as a percent of the
// whole machine since the previous call, so the first call leaves that group
// out, and by memory.
func (r *Reader) Read(now time.Time) []metrics.Extra {
	sample, ok := r.source(now)
	if !ok {
		return nil
	}
	var extras []metrics.Extra
	if r.previous != nil && sample.Total > r.lastTotal {
		extras = appendGroup(extras, cpuGroup(sample.Processes, r.previous, sample.Total-r.lastTotal))
	}
	extras = appendGroup(extras, memoryGroup(sample.Processes))

	r.previous = make(map[key]uint64, len(sample.Processes))
	for _, p := range sample.Processes {
		r.previous[key{p.PID, p.Start}] = p.CPU
	}
	r.lastTotal = sample.Total
	return extras
}

func appendGroup(extras []metrics.Extra, group metrics.Extra) []metrics.Extra {
	if len(group.Items) == 0 {
		return extras
	}
	return append(extras, group)
}

type ranked struct {
	process Process
	value   float64
}

// cpuGroup lists the processes that used the most CPU since previous, which
// holds each process's CPU time then; total is the machine's CPU time since.
func cpuGroup(processes []Process, previous map[key]uint64, total uint64) metrics.Extra {
	var busy []ranked
	for _, p := range processes {
		used := p.CPU
		if before, ok := previous[key{p.PID, p.Start}]; ok {
			// A process that started after the previous read used all its time since.
			used = p.CPU - min(before, p.CPU)
		}
		if used > 0 {
			busy = append(busy, ranked{p, min(100, float64(used)/float64(total)*100)})
		}
	}
	return group(metrics.Extra{
		ID:     "processes-cpu",
		Title:  "Top processes by CPU",
		Titles: map[string]string{"de": "Prozesse mit der meisten CPU-Last", "fr": "Processus les plus gourmands en CPU", "es": "Procesos con más uso de CPU"},
	}, busy, metrics.UnitPercent)
}

// memoryGroup lists the processes that hold the most memory.
func memoryGroup(processes []Process) metrics.Extra {
	var large []ranked
	for _, p := range processes {
		if p.Memory > 0 {
			large = append(large, ranked{p, float64(p.Memory)})
		}
	}
	return group(metrics.Extra{
		ID:     "processes-memory",
		Title:  "Top processes by memory",
		Titles: map[string]string{"de": "Prozesse mit dem meisten Arbeitsspeicher", "fr": "Processus les plus gourmands en mémoire", "es": "Procesos con más uso de memoria"},
	}, large, metrics.UnitBytes)
}

// group fills g with the Top largest of values, largest first. Each is
// labelled with its process's name, numbered from the second time a name
// shows up, and its id holds the PID. The values are live only, as
// processes come and go.
func group(g metrics.Extra, values []ranked, unit metrics.Unit) metrics.Extra {
	slices.SortStableFunc(values, func(a, b ranked) int {
		return cmp.Or(cmp.Compare(b.value, a.value), cmp.Compare(a.process.PID, b.process.PID))
	})
	seen := map[string]int{}
	for _, v := range values[:min(Top, len(values))] {
		name := v.process.Name
		if name == "" {
			name = strconv.Itoa(v.process.PID)
		}
		seen[name]++
		label := name
		if n := seen[name]; n > 1 {
			label = fmt.Sprintf("%s (%d)", name, n)
		}
		value := v.value
		g.Items = append(g.Items, metrics.ExtraItem{
			ID:    "pid-" + strconv.Itoa(v.process.PID),
			Label: label,
			Unit:  unit,
			Value: &value,
		})
	}
	return g
}
