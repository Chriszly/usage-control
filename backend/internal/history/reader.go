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
	// Device is the name the readings are stored under; LocalDevice when empty.
	Device string
}

// Range returns the values from from up to (not including) to, averaged over
// steps so there are at most maxPoints points per metric, and the step.
func (r Reader) Range(ctx context.Context, from, to time.Time) ([]Series, time.Duration, error) {
	span := to.Sub(from)
	if span <= RecentSpan {
		step := stepFor(span, RecentInterval)
		return r.Recent.Range(from, to, step), step, nil
	}
	step := stepFor(span, SampleInterval)
	series, err := r.Store.Range(ctx, deviceOrLocal(r.Device), from, to, step)
	return series, step, err
}

// stepFor returns the step that splits span into at most maxPoints steps: a
// whole number of intervals between readings, so every step averages the
// same number of them.
func stepFor(span, interval time.Duration) time.Duration {
	readings := (span/interval + maxPoints - 1) / maxPoints
	return max(1, readings) * interval
}

// deviceOrLocal returns device, or LocalDevice when it is empty.
func deviceOrLocal(device string) string {
	if device == "" {
		return LocalDevice
	}
	return device
}
