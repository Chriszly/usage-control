package pressure

import "github.com/Chriszly/usage-control/backend/internal/metrics"

// Windows keeps no pressure stall information: nothing in it measures how
// long tasks waited. The Windows reader reports the closest signals its
// performance counters offer instead, each under an id of its own, since
// they mean something else than the Linux values:
//
//   - CPU: \System\Processor Queue Length, the number of threads ready to run
//     that wait for a processor, at the moment of the reading.
//   - Memory: \Memory\Pages Input/sec, the pages read from disk per second to
//     resolve hard page faults, each of which makes a thread wait for memory
//     that had been paged out (or for a mapped file to be read in). Free
//     memory (\Memory\Available Bytes) says only how much is left, not
//     whether anything waits, so it is not used.
//   - Disks: 100 minus \PhysicalDisk(_Total)\% Idle Time, the share of the
//     time the disks had work, averaged over all disks.

// counters are the values of the Windows performance counters; a counter that
// could not be read is nil.
type counters struct {
	processorQueue *float64
	pagesInput     *float64
	diskIdle       *float64
}

// signal is one value the Windows reader reports.
type signal struct {
	id, label string
	labels    map[string]string
	unit      metrics.Unit
}

var (
	cpuQueue = signal{"cpu-queue", "CPU: threads waiting", map[string]string{
		"de": "CPU: wartende Threads", "fr": "Processeur : threads en attente", "es": "CPU: hilos en espera",
	}, metrics.UnitNumber}
	memoryHardFaults = signal{"memory-hard-faults", "Memory: pages read from disk", map[string]string{
		"de": "Arbeitsspeicher: vom Datenträger gelesene Seiten", "fr": "Mémoire : pages lues sur le disque", "es": "Memoria: páginas leídas del disco",
	}, metrics.UnitPerSecond}
	diskBusy = signal{"disk-busy", "Disks: busy", map[string]string{
		"de": "Datenträger: ausgelastet", "fr": "Disques : occupés", "es": "Discos: ocupados",
	}, metrics.UnitPercent}
)

// group returns the group of extras the add-on reports, without items.
func group() metrics.Extra {
	return metrics.Extra{
		ID:     "pressure",
		Title:  "Pressure",
		Titles: map[string]string{"de": "Engpässe", "fr": "Saturation", "es": "Saturación"},
	}
}

// fromCounters returns what the Windows performance counters read as the
// group of extras the collector shows, or nothing when none could be read.
func fromCounters(c counters) []metrics.Extra {
	g := group()
	add := func(v signal, value float64) {
		g.Items = append(g.Items, metrics.ExtraItem{
			ID: v.id, Label: v.label, Labels: v.labels, Unit: v.unit, Value: &value, History: true,
		})
	}
	if c.processorQueue != nil && *c.processorQueue >= 0 {
		add(cpuQueue, *c.processorQueue)
	}
	if c.pagesInput != nil && *c.pagesInput >= 0 {
		add(memoryHardFaults, *c.pagesInput)
	}
	if c.diskIdle != nil {
		add(diskBusy, min(max(100-*c.diskIdle, 0), 100))
	}
	if len(g.Items) == 0 {
		return nil
	}
	return []metrics.Extra{g}
}
