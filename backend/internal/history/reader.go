package history

import (
	"cmp"
	"context"
	"slices"
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
// A range of up to RecentSpan comes from the readings in memory. Where they
// have a gap, as while a hub could not reach the device, or do not reach
// back to from yet, as after a restart, the steps of the gap get the values
// of the minutes the database has of that time. Only a range without any
// reading in memory comes from the database in SampleInterval steps, like a
// longer range.
func (r Reader) Range(ctx context.Context, from, to time.Time) ([]Series, time.Duration, error) {
	span := to.Sub(from)
	if span <= RecentSpan {
		step := stepFor(span, RecentInterval)
		if r.Recent.Covers(from) {
			return r.Recent.Range(from, to, step), step, nil
		}
		if missing, ok := r.Recent.missing(from, to, step); ok {
			series := r.Recent.Range(from, to, step)
			if len(missing) == 0 {
				return series, step, nil
			}
			// From the start of the minute from falls in, whose value the
			// first steps may get.
			stored, err := r.Store.cachedRange(ctx, r.Device, from.Truncate(SampleInterval), to, SampleInterval)
			if err != nil {
				return nil, 0, err
			}
			return fillGaps(series, stored, missing), step, nil
		}
	}
	step := stepFor(span, SampleInterval)
	series, err := r.Store.cachedRange(ctx, r.Device, from, to, step)
	return series, step, err
}

// fillGaps adds to series, read from memory, a point for each of the missing
// steps that the database's values in SampleInterval steps, stored, have a
// value for: the value of the minute the step falls in. stored may be shared
// with the cache, so it is left as it is.
func fillGaps(series, stored []Series, missing []int64) []Series {
	minute := int64(SampleInterval / time.Second)
	index := make(map[string]int, len(series))
	for i, s := range series {
		index[s.Metric] = i
	}
	for _, s := range stored {
		byMinute := make(map[int64]float64, len(s.Points))
		for _, p := range s.Points {
			byMinute[p.Time] = p.Value
		}
		var points []Point
		for _, start := range missing {
			if value, ok := byMinute[start/minute*minute]; ok {
				points = append(points, Point{Time: start, Value: value})
			}
		}
		if len(points) == 0 {
			continue
		}
		i, ok := index[s.Metric]
		if !ok {
			i = len(series)
			index[s.Metric] = i
			series = append(series, Series{Metric: s.Metric})
		}
		series[i].Points = append(series[i].Points, points...)
		slices.SortFunc(series[i].Points, func(a, b Point) int { return cmp.Compare(a.Time, b.Time) })
	}
	slices.SortFunc(series, func(a, b Series) int { return cmp.Compare(a.Metric, b.Metric) })
	return series
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
