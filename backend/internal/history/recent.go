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

// Recent keeps the readings of the last RecentSpan in memory. It is empty
// after a restart and fills up again within RecentSpan.
type Recent struct {
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
	oldest := at.Add(-RecentSpan).Unix()
	r.readings = slices.DeleteFunc(r.readings, func(x reading) bool { return x.time < oldest })
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
