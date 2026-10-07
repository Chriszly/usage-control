package smart

import (
	"fmt"
	"hash/crc32"
	"log/slog"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// value is one kind of value of a disk, with what it is called.
type value struct {
	id     string
	label  string
	labels map[string]string
	unit   metrics.Unit
	get    func(Disk) *float64
}

var values = []value{
	{
		id: "temperature", label: "Temperature", unit: metrics.UnitCelsius,
		labels: map[string]string{"de": "Temperatur", "fr": "Température", "es": "Temperatura"},
		get:    func(d Disk) *float64 { return d.Celsius },
	},
	{
		id: "power-on-hours", label: "Power-on hours", unit: metrics.UnitNumber,
		labels: map[string]string{"de": "Betriebsstunden", "fr": "Heures de fonctionnement", "es": "Horas de funcionamiento"},
		get:    func(d Disk) *float64 { return d.PowerOnHours },
	},
	{
		id: "reallocated", label: "Reallocated sectors", unit: metrics.UnitNumber,
		labels: map[string]string{"de": "Ersetzte Sektoren", "fr": "Secteurs réalloués", "es": "Sectores reasignados"},
		get:    func(d Disk) *float64 { return d.ReallocatedSectors },
	},
	{
		id: "media-errors", label: "Media errors", unit: metrics.UnitNumber,
		labels: map[string]string{"de": "Medienfehler", "fr": "Erreurs de support", "es": "Errores de medio"},
		get:    func(d Disk) *float64 { return d.MediaErrors },
	},
	{
		id: "used", label: "Wear", unit: metrics.UnitPercent,
		labels: map[string]string{"de": "Verschleiß", "fr": "Usure", "es": "Desgaste"},
		get:    func(d Disk) *float64 { return d.PercentageUsed },
	},
}

// The translations of the disk's own check, of a disk that cannot be read,
// and of one with SMART switched off. Only labels have translations, so the label tells whether it passed
// and the text is a mark that needs none.
var (
	passedLabels     = map[string]string{"de": "SMART-Prüfung bestanden", "fr": "Contrôle SMART réussi", "es": "Comprobación SMART superada"}
	failedLabels     = map[string]string{"de": "SMART-Prüfung NICHT BESTANDEN", "fr": "Contrôle SMART ÉCHOUÉ", "es": "Comprobación SMART FALLIDA"}
	unreadableLabels = map[string]string{"de": "Kann nicht gelesen werden", "fr": "Ne peut pas être lu", "es": "No se puede leer"}
	smartOffLabels   = map[string]string{"de": "SMART aus", "fr": "SMART désactivé", "es": "SMART desactivado"}
)

// defaultMaxEntries is how many values of a group usage-control keeps unless
// HISTORY_MAX_ENTRIES is set higher.
const defaultMaxEntries = 64

// warnedTooMany is whether Extras has logged that the disks report more
// values than defaultMaxEntries.
var warnedTooMany atomic.Bool

// Extras returns the disks as the group of extras the collector shows: for
// each disk its overall check and the numbers it reports, each labelled with
// the disk, such as "Samsung SSD 980 (nvme0): Temperature", or for a disk
// that cannot be read any more or has SMART switched off only that, in place
// of its check. The values of all disks are in one group, whose id is part of
// the name their history is kept under; past defaultMaxEntries values, which
// about 16 SATA disks reach, usage-control leaves out the rest unless
// HISTORY_MAX_ENTRIES is set higher, which is logged once.
func Extras(disks []Disk) []metrics.Extra {
	group := metrics.Extra{
		ID:     "smart",
		Title:  "Disk health",
		Titles: map[string]string{"de": "Laufwerkszustand", "fr": "Santé des disques", "es": "Salud de los discos"},
	}
	used := map[string]bool{}
	for _, disk := range disks {
		id := diskID(disk, used)
		used[id] = true
		prefix := disk.Name
		if disk.Model != "" {
			prefix = disk.Model + " (" + disk.Name + ")"
		}
		if disk.Passed != nil || disk.Unreadable || disk.SMARTOff {
			label, labels, text := "SMART check passed", passedLabels, "✓"
			switch {
			case disk.Unreadable:
				label, labels, text = "Cannot be read", unreadableLabels, "✗"
			case disk.SMARTOff:
				label, labels, text = "SMART off", smartOffLabels, "–"
			case !*disk.Passed:
				label, labels, text = "SMART check FAILED", failedLabels, "✗"
			}
			group.Items = append(group.Items, metrics.ExtraItem{
				ID:     id + "-health",
				Label:  prefix + ": " + label,
				Labels: labelled(prefix, labels),
				Unit:   metrics.UnitText,
				Text:   text,
			})
		}
		for _, v := range values {
			number := v.get(disk)
			if number == nil {
				continue
			}
			n := *number
			group.Items = append(group.Items, metrics.ExtraItem{
				ID:      id + "-" + v.id,
				Label:   prefix + ": " + v.label,
				Labels:  labelled(prefix, v.labels),
				Unit:    v.unit,
				Value:   &n,
				History: true,
			})
		}
	}
	if len(group.Items) == 0 {
		return nil
	}
	if len(group.Items) > defaultMaxEntries && !warnedTooMany.Swap(true) {
		slog.Warn("the disks report more values than usage-control keeps by default, so those of the last disks are left out; set HISTORY_MAX_ENTRIES higher on this device and its hub to keep them",
			"values", len(group.Items), "kept", defaultMaxEntries)
	}
	return []metrics.Extra{group}
}

// labelled puts prefix before each translation.
func labelled(prefix string, labels map[string]string) map[string]string {
	out := make(map[string]string, len(labels))
	for language, label := range labels {
		out[language] = prefix + ": " + label
	}
	return out
}

// diskID returns the start of the ids of a disk's values: its serial
// number, so a disk keeps its history when names such as sda go to other
// disks at a start. A disk without one, or with the same one as a disk
// before it, as some cheap disks report, is told by its name.
func diskID(disk Disk, used map[string]bool) string {
	if id := idOf(disk.Serial); id != "" && !used[id] {
		return id
	}
	if id := idOf(disk.Name); id != "" {
		return id
	}
	return "disk"
}

var notInID = regexp.MustCompile(`[^a-z0-9]+`)

// maxDiskID is the longest start of an id, so that the longest value id,
// with "-power-on-hours", still fits in 40 characters.
const maxDiskID = 40 - len("-power-on-hours")

// idOf turns a serial number or a device name such as "Disk 0" into the
// start of an id: lowercase letters and digits joined by "-", at most
// maxDiskID characters. A longer one is cut and ends in a checksum of the
// whole, so two that start the same keep apart.
func idOf(name string) string {
	id := strings.Trim(notInID.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(id) > maxDiskID {
		sum := crc32.ChecksumIEEE([]byte(id))
		id = fmt.Sprintf("%s-%08x", strings.TrimRight(id[:maxDiskID-9], "-"), sum)
	}
	return id
}
