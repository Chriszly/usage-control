package history

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestRecentRangeAveragesEachStep(t *testing.T) {
	var recent Recent
	start := time.Unix(1_800_000_000, 0) // a multiple of the 10 second step below
	for i, cpu := range []float64{10, 20, 30, 40} {
		recent.Add(start.Add(time.Duration(i)*5*time.Second), map[string]float64{MetricCPU: cpu, MetricMemory: 50})
	}

	got := recent.Range(start, start.Add(20*time.Second), 10*time.Second)

	want := []Series{
		{Metric: MetricCPU, Points: []Point{{start.Unix(), 15}, {start.Unix() + 10, 35}}},
		{Metric: MetricMemory, Points: []Point{{start.Unix(), 50}, {start.Unix() + 10, 50}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Range() = %+v, want %+v", got, want)
	}
}

func TestRecentForgetsOldReadings(t *testing.T) {
	var recent Recent
	now := time.Unix(1_800_000_000, 0)
	recent.Add(now.Add(-RecentSpan-time.Second), map[string]float64{MetricCPU: 99})
	recent.Add(now, map[string]float64{MetricCPU: 1})

	got := recent.Average(now.Add(-time.Hour), now.Add(time.Second))

	if !reflect.DeepEqual(got, map[string]float64{MetricCPU: 1}) {
		t.Errorf("Average() = %v, want only the reading within RecentSpan", got)
	}
	if got := recent.Average(now.Add(time.Second), now.Add(time.Minute)); got != nil {
		t.Errorf("Average() without readings = %v, want nil", got)
	}
}

func TestRecentCoversFromItsOldestReadingOn(t *testing.T) {
	var recent Recent
	oldest := time.Unix(1_800_000_000, 0)
	if recent.Covers(oldest.Add(-time.Hour)) {
		t.Error("Covers() without readings = true, want false")
	}
	recent.Add(oldest, map[string]float64{MetricCPU: 1})
	recent.Add(oldest.Add(RecentInterval), map[string]float64{MetricCPU: 2})

	for from, want := range map[time.Duration]bool{
		time.Minute:         true,
		0:                   true,
		-RecentInterval:     true, // from falls between two readings before the oldest
		-RecentInterval - 1: false,
		-time.Minute:        false,
	} {
		if got := recent.Covers(oldest.Add(from)); got != want {
			t.Errorf("Covers(oldest %+v) = %v, want %v", from, got, want)
		}
	}
}

func TestReaderReadsShortRangesFromTheDatabaseUntilMemoryCoversThem(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Now().Truncate(time.Minute)
	if err := store.Add(ctx, LocalDevice, now.Add(-5*time.Minute), map[string]float64{MetricCPU: 10}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	// The readings started two minutes ago, as after a restart.
	recent := &Recent{}
	recent.Add(now.Add(-2*time.Minute), map[string]float64{MetricCPU: 20})
	recent.Add(now.Add(-30*time.Second), map[string]float64{MetricCPU: 20})
	reader := Reader{Store: store, Recent: recent, Device: LocalDevice}

	long, step, err := reader.Range(ctx, now.Add(-10*time.Minute), now)
	if err != nil || step != SampleInterval || len(long) != 1 || long[0].Points[0].Value != 10 {
		t.Errorf("Range(10 min) = %+v, %v, %v, want the stored value in 1 minute steps", long, step, err)
	}
	short, step, err := reader.Range(ctx, now.Add(-time.Minute), now)
	if err != nil || step != RecentInterval || len(short) != 1 || short[0].Points[0].Value != 20 {
		t.Errorf("Range(1 min) = %+v, %v, %v, want the reading from memory in 5 second steps", short, step, err)
	}
}

func TestReaderAnswersLongRangesFromTheCacheForAMinute(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Date(2026, 10, 3, 12, 0, 30, 0, time.UTC)
	store.cache.now = func() time.Time { return now }
	if err := store.Add(ctx, LocalDevice, now.Add(-time.Minute), map[string]float64{MetricCPU: 10}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	reader := Reader{Store: store, Recent: &Recent{}, Device: LocalDevice}
	month := 30 * 24 * time.Hour // 2 hour steps

	first, step, err := reader.Range(ctx, now.Add(-month), now)
	if err != nil || step != 2*time.Hour || len(first) != 1 || first[0].Points[0].Value != 10 {
		t.Fatalf("Range(30 d) = %+v, %v, %v, want the stored value in 2 hour steps", first, step, err)
	}
	if err := store.Add(ctx, LocalDevice, now.Add(-2*time.Minute), map[string]float64{MetricCPU: 30}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	// Ten seconds later, within the same steps, the answer is the cached one.
	cached, _, err := reader.Range(ctx, now.Add(-month+10*time.Second), now.Add(10*time.Second))
	if err != nil || !reflect.DeepEqual(cached, first) {
		t.Errorf("Range(30 d) again = %+v, %v; want the cached answer %+v", cached, err, first)
	}
	// A range ending in another step is read again, and so is one asked for a
	// minute later.
	moved, _, err := reader.Range(ctx, now.Add(-month+2*time.Hour), now.Add(2*time.Hour))
	if err != nil || len(moved) != 1 || moved[0].Points[0].Value != 20 {
		t.Errorf("Range(30 d) ending in the next step = %+v, %v; want the average of both values", moved, err)
	}
	now = now.Add(cacheFor)
	later, _, err := reader.Range(ctx, now.Add(-month), now)
	if err != nil || len(later) != 1 || later[0].Points[0].Value != 20 {
		t.Errorf("Range(30 d) a minute later = %+v, %v; want the average of both values", later, err)
	}
}

func TestReaderReadsShortRangesFromMemory(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Now().Truncate(time.Minute)
	if err := store.Add(ctx, LocalDevice, now.Add(-time.Minute), map[string]float64{MetricCPU: 10}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	recent := &Recent{}
	recent.Add(now.Add(-10*time.Minute), map[string]float64{MetricCPU: 20})
	recent.Add(now.Add(-time.Minute), map[string]float64{MetricCPU: 20})
	reader := Reader{Store: store, Recent: recent, Device: LocalDevice}

	short, step, err := reader.Range(ctx, now.Add(-10*time.Minute), now)
	if err != nil || step != RecentInterval || len(short) != 1 || short[0].Points[0].Value != 20 {
		t.Errorf("Range(10 min) = %+v, %v, %v, want the reading from memory in 5 second steps", short, step, err)
	}
	long, step, err := reader.Range(ctx, now.Add(-time.Hour), now)
	if err != nil || step != SampleInterval || len(long) != 1 || long[0].Points[0].Value != 10 {
		t.Errorf("Range(1 h) = %+v, %v, %v, want the stored value in 1 minute steps", long, step, err)
	}
}

func TestStepFor(t *testing.T) {
	tests := []struct {
		span, interval, want time.Duration
	}{
		{time.Minute, RecentInterval, RecentInterval},
		{30 * time.Minute, RecentInterval, RecentInterval},
		{time.Hour, SampleInterval, SampleInterval},
		{6 * time.Hour, SampleInterval, SampleInterval},
		{6*time.Hour + time.Minute, SampleInterval, 2 * time.Minute},
		{30 * 24 * time.Hour, SampleInterval, 2 * time.Hour},
		{365 * 24 * time.Hour, SampleInterval, 25 * time.Hour}, // 24 h 20 min, as whole hours
	}
	for _, tt := range tests {
		if got := stepFor(tt.span, tt.interval); got != tt.want {
			t.Errorf("stepFor(%v, %v) = %v, want %v", tt.span, tt.interval, got, tt.want)
		}
	}
}
