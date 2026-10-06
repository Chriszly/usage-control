package power

import (
	"bufio"
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// pmicTotal reads the power of a Raspberry Pi 5 from its power chip (PMIC),
// through vcgencmd: the sum of each supply rail's current times its voltage.
// Older Pis have no such chip, and vcgencmd answers without values there.
func (r *Reader) pmicTotal(ctx context.Context) []Reading {
	if r.pmic == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	// r.pmic is the vcgencmd found on the PATH at start, and the argument is fixed.
	out, err := exec.CommandContext(ctx, r.pmic, "pmic_read_adc").Output()
	if err != nil {
		return nil
	}
	watts, ok := parsePMIC(string(out))
	if !ok {
		return nil
	}
	return []Reading{{
		ID:     "raspberry-pi",
		Label:  "Raspberry Pi (total)",
		Labels: map[string]string{"de": "Raspberry Pi (gesamt)", "fr": "Raspberry Pi (total)", "es": "Raspberry Pi (total)"},
		Watts:  watts,
	}}
}

// pmicLine matches a line of vcgencmd pmic_read_adc, such as
// "VDD_CORE_A current(7)=2.31090000A" or "VDD_CORE_V volt(15)=0.84000000V".
var pmicLine = regexp.MustCompile(`^\s*(\S+)_([AV])\s+\w+\(\d+\)=([0-9.]+)[AV]\s*$`)

// parsePMIC adds up current times voltage of each rail that reports both. It
// is false when no rail does.
func parsePMIC(out string) (float64, bool) {
	currents := map[string]float64{}
	volts := map[string]float64{}
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		m := pmicLine.FindStringSubmatch(scanner.Text())
		if m == nil {
			continue
		}
		value, err := strconv.ParseFloat(m[3], 64)
		if err != nil {
			continue
		}
		if m[2] == "A" {
			currents[m[1]] = value
		} else {
			volts[m[1]] = value
		}
	}
	total, found := 0.0, false
	for rail, current := range currents {
		if volt, ok := volts[rail]; ok {
			total += current * volt
			found = true
		}
	}
	return total, found
}
