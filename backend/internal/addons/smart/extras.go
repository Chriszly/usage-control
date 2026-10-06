package smart

import (
	"regexp"
	"strings"

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

var healthLabels = map[string]string{"de": "Zustand", "fr": "État", "es": "Estado"}

// Extras returns the disks as the group of extras the collector shows: for
// each disk its overall check as text and the numbers it reports, each
// labelled with the disk, such as "Samsung SSD 980 (nvme0): Temperature".
func Extras(disks []Disk) []metrics.Extra {
	group := metrics.Extra{
		ID:     "smart",
		Title:  "Disk health",
		Titles: map[string]string{"de": "Laufwerkszustand", "fr": "Santé des disques", "es": "Salud de los discos"},
	}
	for _, disk := range disks {
		name := strings.TrimPrefix(disk.Name, "/dev/")
		id := idOf(name)
		prefix := name
		if disk.Model != "" {
			prefix = disk.Model + " (" + name + ")"
		}
		if disk.Passed != nil {
			text := "passed"
			if !*disk.Passed {
				text = "FAILED"
			}
			group.Items = append(group.Items, metrics.ExtraItem{
				ID:     id + "-health",
				Label:  prefix + ": Health",
				Labels: labelled(prefix, healthLabels),
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

var notInID = regexp.MustCompile(`[^a-z0-9]+`)

// idOf turns a device name such as "sda" or "bus/0" into the start of an id:
// lowercase letters and digits joined by "-", short enough that the longest
// value id still fits in 40 characters.
func idOf(name string) string {
	id := strings.Trim(notInID.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(id) > 25 {
		id = strings.TrimRight(id[:25], "-")
	}
	if id == "" {
		id = "disk"
	}
	return id
}
