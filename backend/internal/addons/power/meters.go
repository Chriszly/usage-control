package power

import (
	"cmp"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// This file names the power meters Windows offers. It has no build
// constraint so its tests run on every OS.

// raplMeter matches the instances of the "Energy Meter" performance counters
// that Windows fills from the CPU's RAPL counters, such as RAPL_Package0_PKG.
var raplMeter = regexp.MustCompile(`(?i)^RAPL_Package([0-9]+)_(PKG|PP0|PP1|DRAM|PSYS)$`)

// raplZones names the RAPL parts the way Linux does, in the order Linux
// lists them: PP0 is the cores and PP1 the rest of the CPU, mostly its
// integrated graphics.
var raplZones = map[string]string{"PKG": "package-", "PP0": "core", "PP1": "uncore", "DRAM": "dram", "PSYS": "psys"}

var raplOrder = []string{"PKG", "PP0", "PP1", "DRAM", "PSYS"}

// meterReadings turns the values of the "Energy Meter" Power counter, in
// milliwatts by instance name, into readings: RAPL meters named as on Linux,
// first by package, then any other meter (such as those a laptop's firmware
// offers) under its own name.
func meterReadings(milliwatts map[string]float64) []Reading {
	type meter struct {
		pkg, rank int
		name      string
		reading   Reading
	}
	var meters []meter
	for name, value := range milliwatts {
		if value < 0 || strings.EqualFold(name, "_Total") {
			continue
		}
		m := meter{pkg: -1, name: name, reading: Reading{ID: idOf("meter", name), Label: strings.ReplaceAll(name, "_", " ")}}
		if match := raplMeter.FindStringSubmatch(name); match != nil {
			part := strings.ToUpper(match[2])
			zone := raplZones[part]
			if part == "PKG" {
				zone += match[1]
			}
			m.pkg, _ = strconv.Atoi(match[1])
			m.rank = slices.Index(raplOrder, part)
			m.reading = zoneReading("intel-rapl:"+match[1], zone)
		}
		m.reading.Watts = value / 1000
		meters = append(meters, m)
	}
	slices.SortFunc(meters, func(a, b meter) int {
		// Other meters, with pkg -1, go after the RAPL ones.
		if (a.pkg < 0) != (b.pkg < 0) {
			return cmp.Compare(b.pkg, a.pkg)
		}
		return cmp.Or(cmp.Compare(a.pkg, b.pkg), cmp.Compare(a.rank, b.rank), strings.Compare(a.name, b.name))
	})
	readings := make([]Reading, 0, len(meters))
	for _, m := range meters {
		readings = append(readings, m.reading)
	}
	return readings
}

// Battery states and values of IOCTL_BATTERY_QUERY_STATUS on Windows.
const (
	batteryDischarging = 0x00000002 // in the power state
	batteryUnknownRate = -0x80000000
)

// batteryReading returns the power a battery gives while the PC runs on it,
// from its state and rate, which Windows gives in milliwatts, negative while
// it discharges. While the PC is plugged in, the battery tells how fast it
// charges, which is not what the PC draws, so then there is no reading.
func batteryReading(index int, state uint32, rate int32) (Reading, bool) {
	if state&batteryDischarging == 0 || rate == batteryUnknownRate || rate >= 0 {
		return Reading{}, false
	}
	reading := Reading{ID: idOf("battery", strconv.Itoa(index)), Label: "Battery discharge", Labels: map[string]string{
		"de": "Akku-Entladung", "fr": "Décharge de la batterie", "es": "Descarga de la batería",
	}}
	if index > 0 {
		n := " " + strconv.Itoa(index+1)
		reading.Label += n
		for language := range reading.Labels {
			reading.Labels[language] += n
		}
	}
	reading.Watts = -float64(rate) / 1000
	return reading, true
}
