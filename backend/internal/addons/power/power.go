// Package power reads how much power the machine draws, for the power
// add-on: per CPU package from Intel and AMD RAPL counters, from the kernel's
// hwmon power sensors (such as AMD GPUs), from NVIDIA GPUs through
// nvidia-smi, and in total on a Raspberry Pi 5 from its power chip. On
// Windows it reads the energy meters Windows offers (RAPL and the meters of
// laptops), what the battery gives while the PC runs on it, and NVIDIA GPUs.
//
// It only reads; nothing in here changes the machine.
package power

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// Reading is one power value.
type Reading struct {
	// ID names the value within the add-on's group, such as "rapl-package-0".
	ID string
	// Label is what the value is in English, and Labels the same in other
	// languages.
	Label  string
	Labels map[string]string
	Watts  float64
}

// Reader reads every power value the machine reports.
type Reader struct {
	rapl   *rapl
	hwmon  string
	pmic   string
	nvidia string
	system *system
}

// NewReader returns a Reader for the machine. sysDir is where /sys is, which
// in a container is where the host's /sys is mounted.
func NewReader(sysDir string) *Reader {
	return &Reader{
		rapl:   newRAPL(filepath.Join(sysDir, "class", "powercap")),
		hwmon:  filepath.Join(sysDir, "class", "hwmon"),
		pmic:   lookPath("vcgencmd"),
		nvidia: lookPath("nvidia-smi"),
		system: newSystem(),
	}
}

// Read returns the power values, in a fixed order. Power measured from an
// energy counter is the average since the previous call, so the first call
// leaves those out.
func (r *Reader) Read(ctx context.Context, now time.Time) []Reading {
	var readings []Reading
	readings = append(readings, r.pmicTotal(ctx)...)
	readings = append(readings, r.rapl.read(now)...)
	readings = append(readings, readHwmon(r.hwmon)...)
	readings = append(readings, r.system.read()...)
	readings = append(readings, readNvidia(ctx, r.nvidia)...)
	return readings
}

// Extras returns the readings as the group of extras the collector shows.
// Labels that occur more than once, such as two GPUs of the same model, are
// numbered in every language, so each row can be told apart.
func Extras(readings []Reading) []metrics.Extra {
	if len(readings) == 0 {
		return nil
	}
	group := metrics.Extra{
		ID:     "power",
		Title:  "Power",
		Titles: map[string]string{"de": "Leistungsaufnahme", "fr": "Consommation", "es": "Consumo"},
	}
	names := make([]string, len(readings))
	for i, r := range readings {
		names[i] = r.Label
	}
	numbered := metrics.NumberDuplicates(names)
	for i, r := range readings {
		watts := r.Watts
		label, labels := numberLabels(r.Label, r.Labels, numbered[i])
		group.Items = append(group.Items, metrics.ExtraItem{
			ID:      r.ID,
			Label:   label,
			Labels:  labels,
			Unit:    metrics.UnitWatts,
			Value:   &watts,
			History: true,
		})
	}
	return []metrics.Extra{group}
}

// numberLabels gives the labels in other languages the number that numbered
// added to the English label.
func numberLabels(label string, labels map[string]string, numbered string) (string, map[string]string) {
	if numbered == label || len(labels) == 0 {
		return numbered, labels
	}
	suffix := strings.TrimPrefix(numbered, label)
	out := make(map[string]string, len(labels))
	for lang, text := range labels {
		out[lang] = text + suffix
	}
	return numbered, out
}

// HostSys returns where /sys is: HOST_SYS in a container that mounts the
// host's /sys there, else /sys.
func HostSys() string {
	if dir := os.Getenv("HOST_SYS"); dir != "" {
		return dir
	}
	return "/sys"
}
