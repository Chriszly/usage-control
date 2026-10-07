package history

import (
	"cmp"
	"slices"
	"sync"
	"time"
)

// RecentInterval is how often the recorder reads the machine's usage. The
// readings of the last RecentSpan are kept in memory, so short ranges such as
// the last minute have enough points; the database keeps one average per
// SampleInterval, which is easier on the SD card of a Raspberry Pi.
const (
	RecentInterval = 5 * time.Second
	RecentSpan     = 30 * time.Minute
)

// gapAfter is how far apart two readings may be before the time between them
// counts as a gap in the readings: a few missed ones.
const gapAfter = 4 * RecentInterval

// Recent keeps the readings of the last RecentSpan in memory. It is empty
// after a restart and fills up again within RecentSpan.
type Recent struct {
	// Span, when set, keeps less than RecentSpan, for a device that only
	// needs the readings until it stores their average.
	Span time.Duration

	mu       sync.Mutex
	readings []reading
}

type reading struct {
	time   int64 // Unix time in seconds
	values map[string]float64
}

// Add keeps the values measured at one time and forgets readings older than
// RecentSpan before it.
func (r *Recent) Add(at time.Time, values map[string]float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.readings = append(r.readings, reading{time: at.Unix(), values: values})
	span := RecentSpan
	if r.Span > 0 {
		span = r.Span
	}
	oldest := at.Add(-span).Unix()
	r.readings = slices.DeleteFunc(r.readings, func(x reading) bool { return x.time < oldest })
}

// Covers reports whether the readings reach back to from without a gap, so a
// range from there on is complete. The oldest may be up to RecentInterval
// after from, as the readings are that far apart and from falls between two
// of them. A gap is a few readings missing in a row, as while a hub could
// not reach the device: the database may have that time from the device
// since.
func (r *Recent) Covers(from time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.readings) == 0 || r.readings[0].time > from.Add(RecentInterval).Unix() {
		return false
	}
	maxGap := int64(gapAfter / time.Second)
	for i := 1; i < len(r.readings); i++ {
		if r.readings[i].time >= from.Unix() && r.readings[i].time-r.readings[i-1].time > maxGap {
			return false
		}
	}
	return true
}

// missing returns the steps from from up to to, by their start, that fall in
// a gap in the readings (see Covers) and so have none: before the oldest
// reading, and between two readings more than gapAfter apart. Steps start at
// multiples of step since the Unix epoch, as in Range. ok is false when no
// reading falls in the range at all.
func (r *Recent) missing(from, to time.Time, step time.Duration) (steps []int64, ok bool) {
	stepSeconds := max(1, int64(step/time.Second))
	start, end := from.Unix(), to.Unix()
	maxGap := int64(gapAfter / time.Second)
	// gap adds the steps after the one a reading at after is in, up to the
	// one a reading at until is in, as far as they are in the range.
	gap := func(after, until int64) {
		for s := max(start/stepSeconds, after/stepSeconds+1) * stepSeconds; s < until/stepSeconds*stepSeconds; s += stepSeconds {
			steps = append(steps, s)
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	// previous is the reading before, or a step before from when there is
	// none, so the steps from from on are missing.
	previous, readBefore := start-stepSeconds, false
	for _, x := range r.readings {
		if x.time >= end {
			break
		}
		if x.time >= start {
			switch {
			case !ok && !readBefore && x.time > start+int64(RecentInterval/time.Second):
				gap(previous, x.time)
			case readBefore && x.time-previous > maxGap:
				gap(previous, x.time)
			}
			ok = true
		}
		previous, readBefore = x.time, true
	}
	return steps, ok
}

// Newest returns the time of the newest reading, or false when there is none.
// The readings of a device that stopped answering stay until it answers
// again, as only a new reading makes the old ones go.
func (r *Recent) Newest() (time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.readings) == 0 {
		return time.Time{}, false
	}
	return time.Unix(r.readings[len(r.readings)-1].time, 0), true
}

// Average returns the average of each metric over the readings from from up
// to (not including) to, or nil when there are none.
func (r *Recent) Average(from, to time.Time) map[string]float64 {
	sums := map[string]float64{}
	counts := map[string]int{}
	r.mu.Lock()
	for _, x := range r.readings {
		if x.time < from.Unix() || x.time >= to.Unix() {
			continue
		}
		for metric, value := range x.values {
			sums[metric] += value
			counts[metric]++
		}
	}
	r.mu.Unlock()

	if len(sums) == 0 {
		return nil
	}
	for metric := range sums {
		sums[metric] /= float64(counts[metric])
	}
	return sums
}

// Range returns the readings from from up to (not including) to, averaged
// over steps of the given length, in the same shape as Store.Range.
// Steps start at multiples of step since the Unix epoch, as in the database.
func (r *Recent) Range(from, to time.Time, step time.Duration) []Series {
	stepSeconds := max(1, int64(step/time.Second))
	type bucket struct {
		sum   float64
		count int
	}
	buckets := map[string]map[int64]*bucket{}

	r.mu.Lock()
	for _, x := range r.readings {
		if x.time < from.Unix() || x.time >= to.Unix() {
			continue
		}
		start := x.time / stepSeconds * stepSeconds
		for metric, value := range x.values {
			if buckets[metric] == nil {
				buckets[metric] = map[int64]*bucket{}
			}
			b := buckets[metric][start]
			if b == nil {
				b = &bucket{}
				buckets[metric][start] = b
			}
			b.sum += value
			b.count++
		}
	}
	r.mu.Unlock()

	series := make([]Series, 0, len(buckets))
	for metric, byStart := range buckets {
		s := Series{Metric: metric}
		for start, b := range byStart {
			s.Points = append(s.Points, Point{Time: start, Value: b.sum / float64(b.count)})
		}
		slices.SortFunc(s.Points, func(a, b Point) int { return cmp.Compare(a.Time, b.Time) })
		series = append(series, s)
	}
	slices.SortFunc(series, func(a, b Series) int { return cmp.Compare(a.Metric, b.Metric) })
	return series
}
