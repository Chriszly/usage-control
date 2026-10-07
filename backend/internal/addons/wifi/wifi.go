// Package wifi reads the link quality and signal of each connected wireless
// interface, for the Wi-Fi add-on: on Linux from /proc/net/wireless, which
// lists an interface only while it is connected, and on Windows through its
// Native Wifi API (see wlan.go). macOS reports nothing.
//
// It only reads; nothing in here changes the machine.
package wifi

import (
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// Reading is what one wireless interface reports.
type Reading struct {
	// Interface names the interface: its name on Linux, such as wlan0, and
	// the adapter's description on Windows.
	Interface string
	// Key tells the interface apart from every other one and stays the same
	// across reboots: its name on Linux and its interface GUID on Windows,
	// where two adapters of the same model have the same description.
	Key string
	// QualityPercent is the link quality from 0 to 100, as the system rates
	// it: the driver's link quality on Linux, the signal quality on Windows.
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
		reading := Reading{Interface: name, Key: name}
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
			ID:    idOf(r.Key, "-quality"),
			Label: label(r.Interface, "link quality"),
			Labels: map[string]string{
				"de": label(r.Interface, "Verbindungsqualität"),
				"fr": label(r.Interface, "qualité du lien"),
				"es": label(r.Interface, "calidad del enlace"),
			},
			Unit:    metrics.UnitPercent,
			Value:   &quality,
			History: true,
		})
		if !r.HasSignal {
			continue
		}
		// The signal is shown as it is now, without history: its chart
		// starts at 0 and has no room for values below it. The quality,
		// which follows from the signal, is the one charted.
		signal := r.SignalDBm
		group.Items = append(group.Items, metrics.ExtraItem{
			ID:    idOf(r.Key, "-signal"),
			Label: label(r.Interface, "signal (dBm)"),
			Labels: map[string]string{
				"de": label(r.Interface, "Signal (dBm)"),
				"fr": label(r.Interface, "signal (dBm)"),
				"es": label(r.Interface, "señal (dBm)"),
			},
			Unit:  metrics.UnitNumber,
			Value: &signal,
		})
	}
	return []metrics.Extra{group}
}

// maxLabel is the most characters of a label metrics.CleanExtras keeps.
const maxLabel = 80

// label joins an interface's name and what a value of it is, as in "wlan0
// link quality". A name too long for both to fit in maxLabel characters, as
// a Windows adapter's description can be, is cut and ends in "…", so what
// tells the values of the interface apart is never cut off. The number
// Windows gives the second and further adapters of a model, as in "… Network
// Adapter #2", is kept after the "…", so their labels stay apart.
func label(name, what string) string {
	room := maxLabel - 1 - utf8.RuneCountInString(what)
	if runes := []rune(name); len(runes) > room {
		number := []rune(windowsNumber.FindString(name))
		keep := room - 1 - len(number)
		name = strings.TrimRight(string(runes[:keep]), " ") + "…" + string(number)
	}
	return name + " " + what
}

// windowsNumber is the number Windows adds to the description of a second
// adapter of the same model, such as " #2".
var windowsNumber = regexp.MustCompile(` #[0-9]+$`)

// HostProc returns where /proc is: HOST_PROC in a container that mounts the
// host's /proc there, else /proc.
func HostProc() string {
	if dir := os.Getenv("HOST_PROC"); dir != "" {
		return dir
	}
	return "/proc"
}

var notInID = regexp.MustCompile(`[^a-z0-9]+`)

// idOf turns an interface's key into an id for an extra that ends in suffix
// ("-quality" or "-signal"): lowercase letters and digits joined by "-", at
// most 40 characters. Keys fit: Linux's interface names have at most 15
// characters and a Windows GUID 32. A longer one is cut the same for both
// suffixes, never the suffix, and ends in a checksum of the whole, so the ids
// of one interface stay alike and apart from those of others.
func idOf(key, suffix string) string {
	id := strings.Trim(notInID.ReplaceAllString(strings.ToLower(key), "-"), "-")
	if limit := 40 - len("-quality"); len(id) > limit {
		sum := crc32.ChecksumIEEE([]byte(id))
		id = fmt.Sprintf("%s-%08x", strings.TrimRight(id[:limit-9], "-"), sum)
	}
	if id == "" {
		id = "wifi"
	}
	return id + suffix
}
