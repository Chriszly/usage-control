package router

import "time"

// maxBytesPerSecond is the fastest traffic a router is taken to have, 100
// Gbit/s: a counter that grew faster than that between two readings was
// reset, not counted up.
const maxBytesPerSecond = 100e9 / 8

// counter turns a byte counter a router reports, which only grows, into
// bytes per second since the previous reading. Many routers count in 32
// bits, so a counter that went down by less than it can hold wrapped around;
// one that went down otherwise, or after the router restarted, was reset,
// and that interval is skipped.
type counter struct {
	value uint64
	at    time.Time
	known bool
}

// rate takes a new reading of the counter at now and returns the bytes per
// second since the previous one, or false when there is none to compare
// with: the first reading, a reset, or a restart of the router (restarted).
func (c *counter) rate(value uint64, now time.Time, restarted bool) (float64, bool) {
	previous, at, known := c.value, c.at, c.known
	c.value, c.at, c.known = value, now, true
	seconds := now.Sub(at).Seconds()
	if !known || restarted || seconds <= 0 {
		return 0, false
	}
	var grown uint64
	switch {
	case value >= previous:
		grown = value - previous
	case previous <= 1<<32-1:
		// A 32-bit counter that wrapped around.
		grown = value + (1<<32 - previous)
	default:
		return 0, false
	}
	perSecond := float64(grown) / seconds
	if perSecond > maxBytesPerSecond {
		return 0, false
	}
	return perSecond, true
}
