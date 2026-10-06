// Package wifi reads the link quality and signal of each wireless interface,
// for the Wi-Fi add-on, from /proc/net/wireless. Linux lists an interface
// there only while it is connected, so the rest report nothing. Windows and
// macOS have no such file and report nothing either.
//
// It only reads; nothing in here changes the machine.
package wifi

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// Reading is what one wireless interface reports.
type Reading struct {
	Interface string
	// QualityPercent is the link quality from 0 to 100.
	QualityPercent float64
	// SignalDBm is the signal level in dBm, when HasSignal is true: drivers
	// that report no level in dBm leave it out.
	SignalDBm float64
	HasSignal bool
}

// File returns where the host's wireless statistics are: in the first
// process of procDir, since /proc/net describes the network of the reading
// process, which in a container or a service without network is not the
// host's.
func File(procDir string) string {
	return filepath.Join(procDir, "1", "net", "wireless")
}

// Read reads the wireless statistics in file, or returns nothing when there
// are none.
func Read(file string) []Reading {
	text, err := os.ReadFile(file) //nolint:gosec // the host's /proc/1/net/wireless, below the folder HOST_PROC names
	if err != nil {
		return nil
	}
	return parse(string(text))
}

// parse reads /proc/net/wireless, whose two header lines are followed by a
// line per interface such as
//
//	wlan0: 0000   54.  -56.  -256        0      0      0      0      0        0
//
// with the status, the link quality, the signal level and the noise level;
// a "." marks a value updated since the last read. Drivers that use the
// kernel's cfg80211, nearly all of them, give the quality out of 70 and the
// level in dBm, or, when their hardware knows no dBm, both out of 100.
func parse(text string) []Reading {
	var readings []Reading
	for line := range strings.Lines(text) {
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		fields := strings.Fields(rest)
		if name == "" || strings.ContainsAny(name, " |") || len(fields) < 3 {
			continue
		}
		quality, errQ := strconv.ParseFloat(strings.TrimSuffix(fields[1], "."), 64)
		level, errL := strconv.ParseFloat(strings.TrimSuffix(fields[2], "."), 64)
		if errQ != nil || errL != nil || (quality == 0 && level == 0) {
			continue
		}
		reading := Reading{Interface: name}
		maximum := 100.0
		if level < 0 {
			maximum = 70
			reading.SignalDBm = level
			reading.HasSignal = true
		}
		reading.QualityPercent = min(100, max(0, quality/maximum*100))
		readings = append(readings, reading)
	}
	return readings
}

// Extras returns the readings as the group of extras the collector shows.
func Extras(readings []Reading) []metrics.Extra {
	if len(readings) == 0 {
		return nil
	}
	group := metrics.Extra{
		ID:     "wifi",
		Title:  "Wi-Fi",
		Titles: map[string]string{"de": "WLAN", "fr": "Wi-Fi", "es": "Wi-Fi"},
	}
	for _, r := range readings {
		quality := r.QualityPercent
		group.Items = append(group.Items, metrics.ExtraItem{
			ID:    idOf(r.Interface, "quality"),
			Label: r.Interface + " link quality",
			Labels: map[string]string{
				"de": r.Interface + " Verbindungsqualität",
				"fr": r.Interface + " qualité du lien",
				"es": r.Interface + " calidad del enlace",
			},
			Unit:    metrics.UnitPercent,
			Value:   &quality,
			History: true,
		})
		if !r.HasSignal {
			continue
		}
		signal := r.SignalDBm
		group.Items = append(group.Items, metrics.ExtraItem{
			ID:    idOf(r.Interface, "signal"),
			Label: r.Interface + " signal (dBm)",
			Labels: map[string]string{
				"de": r.Interface + " Signal (dBm)",
				"fr": r.Interface + " signal (dBm)",
				"es": r.Interface + " señal (dBm)",
			},
			Unit:    metrics.UnitNumber,
			Value:   &signal,
			History: true,
		})
	}
	return []metrics.Extra{group}
}

// HostProc returns where /proc is: HOST_PROC in a container that mounts the
// host's /proc there, else /proc.
func HostProc() string {
	if dir := os.Getenv("HOST_PROC"); dir != "" {
		return dir
	}
	return "/proc"
}

var notInID = regexp.MustCompile(`[^a-z0-9]+`)

// idOf turns parts of a name into an id for an extra: lowercase letters and
// digits joined by "-", at most 40 characters.
func idOf(parts ...string) string {
	id := strings.Trim(notInID.ReplaceAllString(strings.ToLower(strings.Join(parts, "-")), "-"), "-")
	if len(id) > 40 {
		id = strings.TrimRight(id[:40], "-")
	}
	return id
}
