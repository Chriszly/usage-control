package history

import (
	"context"
	"time"
)

// maxPoints is the most points a range has per metric. Longer ranges are
// averaged over longer steps, so a 30 day range stays small.
const maxPoints = 360

// Reader reads a device's usage over time: ranges of up to RecentSpan from
// the readings in memory, longer ones from the database.
type Reader struct {
	Store  *Store
	Recent *Recent
	// Device is the name the readings are stored under, such as LocalDevice.
	Device string
}

// Range returns the values from from up to (not including) to, averaged over
// steps so there are at most maxPoints points per metric, and the step.
// A range of up to RecentSpan comes from the readings in memory, unless they
// do not reach back to from yet, as after a restart: then it comes from the
// database in SampleInterval steps, like a longer range.
func (r Reader) Range(ctx context.Context, from, to time.Time) ([]Series, time.Duration, error) {
	span := to.Sub(from)
	if span <= RecentSpan && r.Recent.Covers(from) {
		step := stepFor(span, RecentInterval)
		return r.Recent.Range(from, to, step), step, nil
	}
	step := stepFor(span, SampleInterval)
	series, err := r.Store.cachedRange(ctx, r.Device, from, to, step)
	return series, step, err
}

// Newest returns the time of the device's newest reading: from memory, or
// from the database when memory has none, as after a restart. It is false
// when there is no reading at all.
func (r Reader) Newest(ctx context.Context) (time.Time, bool, error) {
	if newest, ok := r.Recent.Newest(); ok {
		return newest, true, nil
	}
	return r.Store.Newest(ctx, r.Device)
}

// ExtraInfo returns how the extras in the device's history are described, by
// the metric they are stored under.
func (r Reader) ExtraInfo(ctx context.Context) (map[string]ExtraInfo, error) {
	return r.Store.ExtraInfo(ctx, r.Device)
}

// stepFor returns the step that splits span into at most maxPoints steps: a
// whole number of intervals between readings, so every step averages the
// same number of them. A step of an hour or more is a whole number of hours,
// as Store.Range reads it from the hourly averages.
func stepFor(span, interval time.Duration) time.Duration {
	readings := (span/interval + maxPoints - 1) / maxPoints
	step := max(1, readings) * interval
	if step >= time.Hour {
		step = (step + time.Hour - 1) / time.Hour * time.Hour
	}
	return step
}
