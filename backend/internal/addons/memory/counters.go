package memory

import (
	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// This file turns the memory counters of Windows into extras. It has no build
// constraint so its tests run on every OS; counters_windows.go reads them.

// counter is a performance counter of the Memory object of Windows. Windows
// has no counter for Linux's writeback, shared memory or page tables, so those
// are left out; the counters that differ from their Linux counterpart have ids
// of their own and say what they measure.
type counter struct {
	path   string // the English counter path, as PdhAddEnglishCounter takes it
	id     string
	label  string
	labels map[string]string
	// rate is a counter per second, which Windows measures between two
	// readings and which is shown as events per second.
	rate bool
	// pages is a counter of pages per second, shown in bytes per second.
	pages bool
}

var counters = []counter{
	// Pages changed in memory but not yet written to the page file or the
	// file they belong to: what Linux calls dirty, without its own id since
	// Windows counts also pages that wait for the page file.
	{`\Memory\Modified Page List Bytes`, "modified", "Modified (waiting to be written)", map[string]string{"de": "Geändert, ungeschrieben (Modified)", "fr": "Modifiée, pas encore écrite (Modified)", "es": "Modificada, sin escribir (Modified)"}, false, false},
	// The kernel's memory pools, the nearest Windows has to Linux's slab.
	{`\Memory\Pool Paged Bytes`, "pool-paged", "Kernel paged pool", map[string]string{"de": "Kernel-Pool, auslagerbar", "fr": "Pool paginé du noyau", "es": "Bloque paginado del núcleo"}, false, false},
	{`\Memory\Pool Nonpaged Bytes`, "pool-nonpaged", "Kernel nonpaged pool", map[string]string{"de": "Kernel-Pool, nicht auslagerbar", "fr": "Pool non paginé du noyau", "es": "Bloque no paginado del núcleo"}, false, false},
	{`\Memory\Committed Bytes`, committed.id, committed.label, committed.labels, false, false},
	{`\Memory\Page Faults/sec`, pageFaults.id, pageFaults.label, pageFaults.labels, true, false},
	// Reads from disk to resolve hard page faults; one read can bring in
	// several pages, so it counts at most as many as Linux's major faults.
	{`\Memory\Page Reads/sec`, "page-reads", "Disk reads for page faults", map[string]string{"de": "Lesezugriffe für Seitenfehler", "fr": "Lectures disque pour défauts de page", "es": "Lecturas de disco por fallos de página"}, true, false},
	// Pages read from and written to disk, both the page file and files
	// mapped into memory, so more than Linux's swapping.
	{`\Memory\Pages Input/sec`, "paged-in", "Paged in (page file and mapped files)", map[string]string{"de": "Eingelagert (Auslagerungsdatei und Dateien)", "fr": "Pages lues (fichier d'échange et fichiers)", "es": "Páginas leídas (archivo de paginación y archivos)"}, true, true},
	{`\Memory\Pages Output/sec`, "paged-out", "Paged out (page file and mapped files)", map[string]string{"de": "Ausgelagert (Auslagerungsdatei und Dateien)", "fr": "Pages écrites (fichier d'échange et fichiers)", "es": "Páginas escritas (archivo de paginación y archivos)"}, true, true},
}

// counterItems returns the items for the values of the counters, by counter
// path, leaving out counters without a value. The rates are left out unless
// withRates, since Windows needs two readings to measure them.
func counterItems(values map[string]float64, withRates bool, pageSize float64) []metrics.ExtraItem {
	var items []metrics.ExtraItem
	for _, c := range counters {
		value, ok := values[c.path]
		if !ok || value < 0 || (c.rate && !withRates) {
			continue
		}
		unit := metrics.UnitBytes
		switch {
		case c.pages:
			value *= pageSize
			unit = metrics.UnitBytesPerSecond
		case c.rate:
			unit = metrics.UnitPerSecond
		}
		items = append(items, metrics.ExtraItem{
			ID: c.id, Label: c.label, Labels: c.labels, Unit: unit, Value: &value, History: true,
		})
	}
	return items
}
