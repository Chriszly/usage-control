package power

import (
	"fmt"
	"hash/crc32"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/addons"
)

// lastReadings keeps the readings of a program the add-on starts, vcgencmd
// or nvidia-smi, for interval, or addons.ProgramInterval when it is 0.
type lastReadings struct {
	interval time.Duration
	readings []Reading
	at       time.Time
}

// get returns the kept readings while they are younger than the interval at
// now, and otherwise those read returns.
func (l *lastReadings) get(now time.Time, read func() []Reading) []Reading {
	interval := l.interval
	if interval == 0 {
		interval = addons.ProgramInterval
	}
	if l.at.IsZero() || now.Sub(l.at) >= interval {
		l.readings, l.at = read(), now
	}
	return l.readings
}

// lookPath returns where a program is, or "" when it is not installed.
func lookPath(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return path
}

var notInID = regexp.MustCompile(`[^a-z0-9]+`)

// idOf turns parts of a name into an id for an extra: lowercase letters and
// digits joined by "-", at most 40 characters. A longer one is cut and ends
// in a checksum of the whole, so two names that start the same keep apart.
func idOf(parts ...string) string {
	id := strings.Trim(notInID.ReplaceAllString(strings.ToLower(strings.Join(parts, "-")), "-"), "-")
	if len(id) > 40 {
		sum := crc32.ChecksumIEEE([]byte(id))
		id = fmt.Sprintf("%s-%08x", strings.TrimRight(id[:31], "-"), sum)
	}
	return id
}
