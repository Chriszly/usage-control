package power

import (
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// rapl reads the energy counters of Intel and AMD CPUs (RAPL) under
// /sys/class/powercap. Newer kernels let only root read them; without that
// right, rapl reads nothing.
type rapl struct {
	dir string
	// previous is each zone's counter at the previous read, by folder.
	previous map[string]uint64
	at       time.Time
}

func newRAPL(dir string) *rapl {
	return &rapl{dir: dir}
}

// read returns the average power of each zone since the previous read.
func (r *rapl) read(now time.Time) []Reading {
	zones, _ := filepath.Glob(filepath.Join(r.dir, "intel-rapl:*"))
	slices.Sort(zones)
	current := map[string]uint64{}
	var readings []Reading
	seconds := now.Sub(r.at).Seconds()
	for _, zone := range zones {
		energy, ok := readUint(filepath.Join(zone, "energy_uj"))
		if !ok {
			continue
		}
		current[zone] = energy
		before, known := r.previous[zone]
		if !known || seconds <= 0 {
			continue
		}
		used := energy - before
		if energy < before {
			// The counter wrapped around at its maximum.
			limit, ok := readUint(filepath.Join(zone, "max_energy_range_uj"))
			if !ok || limit < before {
				continue
			}
			used = limit - before + energy
		}
		reading := zoneReading(filepath.Base(zone), readText(filepath.Join(zone, "name")))
		reading.Watts = float64(used) / 1e6 / seconds
		readings = append(readings, reading)
	}
	r.previous, r.at = current, now
	return readings
}

// zoneReading names a RAPL zone, such as intel-rapl:0 named package-0 or
// intel-rapl:0:2 named dram, the memory of package 0.
func zoneReading(folder, name string) Reading {
	id := idOf("rapl", strings.TrimPrefix(folder, "intel-rapl:"), name)
	switch {
	case strings.HasPrefix(name, "package-"):
		n := strings.TrimPrefix(name, "package-")
		return Reading{ID: id, Label: "CPU package " + n, Labels: map[string]string{
			"de": "CPU-Paket " + n, "fr": "Processeur " + n, "es": "Procesador " + n,
		}}
	case name == "core":
		return Reading{ID: id, Label: "CPU cores", Labels: map[string]string{
			"de": "CPU-Kerne", "fr": "Cœurs du processeur", "es": "Núcleos del procesador",
		}}
	case name == "dram":
		return Reading{ID: id, Label: "Memory", Labels: map[string]string{
			"de": "Arbeitsspeicher", "fr": "Mémoire", "es": "Memoria",
		}}
	case name == "psys":
		return Reading{ID: id, Label: "Platform", Labels: map[string]string{
			"de": "Plattform", "fr": "Plateforme", "es": "Plataforma",
		}}
	default:
		// Such as uncore, the integrated graphics; the kernel's name is kept.
		return Reading{ID: id, Label: "CPU " + name}
	}
}
