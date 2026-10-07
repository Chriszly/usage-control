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
	// Time is when the sample was taken, before its processes were read, in
	// the unit of Process.Start and from the same point: it tells a process
	// that started after the previous sample from one that could not be read
	// then.
	Time uint64
}

// Source takes a Sample, or is false when the machine offers none.
type Source func(now time.Time) (Sample, bool)

type key struct {
	pid   int
	start uint64
}

// Reader reads the busiest processes from a Source.
type Reader struct {
	source Source
	// previous is each process's CPU time at the previous sample, and
	// lastTotal and lastTime that sample's Total and Time.
	previous  map[key]uint64
	lastTotal uint64
	lastTime  uint64
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
		extras = appendGroup(extras, cpuGroup(sample.Processes, r.previous, r.lastTime, sample.Total-r.lastTotal))
	}
	extras = appendGroup(extras, memoryGroup(sample.Processes))

	r.previous = cpuTimes(sample.Processes)
	r.lastTotal, r.lastTime = sample.Total, sample.Time
	return extras
}

// cpuTimes returns each process's CPU time.
func cpuTimes(processes []Process) map[key]uint64 {
	times := make(map[key]uint64, len(processes))
	for _, p := range processes {
		times[key{p.PID, p.Start}] = p.CPU
	}
	return times
}

// used returns the CPU time p used since the previous sample, taken at
// since, when previous held each process's CPU time. A process that started
// after it used all its time since. One that is older but was not in it, as
// when it could not be read then, is false: what it used since is not known
// until the next sample.
func used(p Process, previous map[key]uint64, since uint64) (uint64, bool) {
	if before, ok := previous[key{p.PID, p.Start}]; ok {
		return p.CPU - min(before, p.CPU), true
	}
	return p.CPU, p.Start >= since
}

// cpuSum adds up the CPU time of a machine whose system keeps no count of
// it, as Windows, the way Task Manager does: from one sample to the next,
// the time the CPUs were idle plus the time every process used. It needs
// neither the clock nor the number of CPUs.
type cpuSum struct {
	previous map[key]uint64
	idle     uint64
	at       uint64
	total    uint64
}

// add returns the machine's CPU time from the first call until now, when
// the CPUs had been idle for idle and processes were running; now is in the
// unit of Process.Start.
func (s *cpuSum) add(now, idle uint64, processes []Process) uint64 {
	if s.previous != nil {
		s.total += idle - min(s.idle, idle)
		for _, p := range processes {
			if spent, ok := used(p, s.previous, s.at); ok {
				s.total += spent
			}
		}
	}
	s.previous, s.idle, s.at = cpuTimes(processes), idle, now
	return s.total
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

// cpuGroup lists the processes that used the most CPU since the previous
// sample, taken at since, when previous held each process's CPU time; total
// is the machine's CPU time since.
func cpuGroup(processes []Process, previous map[key]uint64, since, total uint64) metrics.Extra {
	var busy []ranked
	for _, p := range processes {
		if spent, ok := used(p, previous, since); ok && spent > 0 {
			busy = append(busy, ranked{p, min(100, float64(spent)/float64(total)*100)})
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
