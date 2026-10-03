package metrics

import "time"

// TimeZone is the time zone the machine's clock is set to: its short name,
// such as "CEST", and how far it is ahead of UTC.
type TimeZone struct {
	Name          string `json:"name"`
	OffsetSeconds int    `json:"offsetSeconds"`
}

// timeZone returns the time zone of t. In a container this is the zone of the
// container, which is UTC unless the host's /etc/localtime is mounted.
func timeZone(t time.Time) *TimeZone {
	name, offset := t.Zone()
	return &TimeZone{Name: name, OffsetSeconds: offset}
}
