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

func TestReaderReadsShortRangesFromMemory(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Now().Truncate(time.Minute)
	if err := store.Add(ctx, LocalDevice, now.Add(-time.Minute), map[string]float64{MetricCPU: 10}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	recent := &Recent{}
	recent.Add(now.Add(-time.Minute), map[string]float64{MetricCPU: 20})
	reader := Reader{Store: store, Recent: recent}

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
	}
	for _, tt := range tests {
		if got := stepFor(tt.span, tt.interval); got != tt.want {
			t.Errorf("stepFor(%v, %v) = %v, want %v", tt.span, tt.interval, got, tt.want)
		}
	}
}
