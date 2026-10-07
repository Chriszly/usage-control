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

func TestReaderFillsWhatMemoryDoesNotReachBackToFromTheDatabase(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Now().Truncate(time.Minute)
	if err := store.Add(ctx, LocalDevice, now.Add(-5*time.Minute), map[string]float64{MetricCPU: 10, MetricMemory: 30}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	// The readings started two minutes ago, as after a restart.
	recent := &Recent{}
	fill(recent, now.Add(-2*time.Minute), now.Add(-30*time.Second), map[string]float64{MetricCPU: 20})
	reader := Reader{Store: store, Recent: recent, Device: LocalDevice}

	got, step, err := reader.Range(ctx, now.Add(-10*time.Minute), now)
	if err != nil || step != RecentInterval {
		t.Fatalf("Range(10 min) = %v, %v; want 5 second steps", step, err)
	}
	// The stored minute fills its twelve steps, the readings follow.
	var cpu, memory []Point
	for start := now.Add(-5 * time.Minute); start.Before(now.Add(-4 * time.Minute)); start = start.Add(RecentInterval) {
		cpu = append(cpu, Point{start.Unix(), 10})
		memory = append(memory, Point{start.Unix(), 30})
	}
	for at := now.Add(-2 * time.Minute); !at.After(now.Add(-30 * time.Second)); at = at.Add(RecentInterval) {
		cpu = append(cpu, Point{at.Unix(), 20})
	}
	want := []Series{{Metric: MetricCPU, Points: cpu}, {Metric: MetricMemory, Points: memory}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Range(10 min) = %+v, want %+v", got, want)
	}
}

func TestReaderReadsShortRangesWithoutReadingsFromTheDatabase(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Now().Truncate(time.Minute)
	if err := store.Add(ctx, LocalDevice, now.Add(-5*time.Minute), map[string]float64{MetricCPU: 10}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	reader := Reader{Store: store, Recent: &Recent{}, Device: LocalDevice}

	got, step, err := reader.Range(ctx, now.Add(-10*time.Minute), now)
	if err != nil || step != SampleInterval || len(got) != 1 || len(got[0].Points) != 1 || got[0].Points[0].Value != 10 {
		t.Errorf("Range(10 min) = %+v, %v, %v, want the stored value in 1 minute steps", got, step, err)
	}
}

func TestRecentMissingFindsTheStepsOfGaps(t *testing.T) {
	var recent Recent
	start := time.Unix(1_800_000_000, 0)
	if _, ok := recent.missing(start, start.Add(time.Minute), RecentInterval); ok {
		t.Error("missing() without readings is ok, want not")
	}
	fill(&recent, start, start.Add(10*time.Second), map[string]float64{MetricCPU: 1})
	fill(&recent, start.Add(40*time.Second), start.Add(time.Minute), map[string]float64{MetricCPU: 1})
	if _, ok := recent.missing(start.Add(12*time.Second), start.Add(30*time.Second), RecentInterval); ok {
		t.Error("missing() within a gap is ok, want not, as no reading falls in the range")
	}

	for _, tt := range []struct {
		from, to time.Duration
		want     []int64
	}{
		{0, time.Minute, []int64{15, 20, 25, 30, 35}},
		{12 * time.Second, 45 * time.Second, []int64{15, 20, 25, 30, 35}},
		{-12 * time.Second, 5 * time.Second, []int64{-15, -10, -5}},
		{-5 * time.Second, 5 * time.Second, nil},
		{45 * time.Second, time.Minute, nil},
	} {
		got, ok := recent.missing(start.Add(tt.from), start.Add(tt.to), RecentInterval)
		for i := range got {
			got[i] -= start.Unix()
		}
		if !ok || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("missing(%v, %v) = %v, %v; want %v", tt.from, tt.to, got, ok, tt.want)
		}
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
	fill(recent, now.Add(-10*time.Minute), now.Add(-time.Minute), map[string]float64{MetricCPU: 20})
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

func TestReaderNewestComesFromMemoryAndElseFromTheDatabase(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	recent := &Recent{}
	reader := Reader{Store: store, Recent: recent, Device: "laptop"}
	if _, ok, err := reader.Newest(ctx); err != nil || ok {
		t.Fatalf("Newest() without readings = %v, %v, want none", ok, err)
	}

	stored := time.Unix(1_800_000_000, 0)
	if err := store.Add(ctx, "laptop", stored, map[string]float64{MetricCPU: 10}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if err := store.Add(ctx, "other", stored.Add(time.Hour), map[string]float64{MetricCPU: 10}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if got, ok, err := reader.Newest(ctx); err != nil || !ok || !got.Equal(stored) {
		t.Errorf("Newest() after a restart = %v, %v, %v, want %v from the database", got, ok, err, stored)
	}

	read := stored.Add(30 * time.Second)
	recent.Add(read, map[string]float64{MetricCPU: 20})
	if got, ok, err := reader.Newest(ctx); err != nil || !ok || !got.Equal(read) {
		t.Errorf("Newest() = %v, %v, %v, want %v from memory", got, ok, err, read)
	}
}

// fill adds the same values every RecentInterval from from up to and
// including to, as the recorder does.
func fill(recent *Recent, from, to time.Time, values map[string]float64) {
	for at := from; !at.After(to); at = at.Add(RecentInterval) {
		recent.Add(at, values)
	}
}

func TestReaderReadsShortRangesWithAGapInMemoryFromTheDatabase(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Now().Truncate(time.Minute)
	// The hub could not reach the device for five minutes, and fetched the
	// minutes it kept meanwhile into the database.
	recent := &Recent{}
	fill(recent, now.Add(-20*time.Minute), now.Add(-10*time.Minute), map[string]float64{MetricCPU: 20})
	fill(recent, now.Add(-5*time.Minute), now, map[string]float64{MetricCPU: 20})
	for at := now.Add(-20 * time.Minute); !at.After(now); at = at.Add(time.Minute) {
		if err := store.Add(ctx, LocalDevice, at, map[string]float64{MetricCPU: 10}); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}
	reader := Reader{Store: store, Recent: recent, Device: LocalDevice}

	across, step, err := reader.Range(ctx, now.Add(-15*time.Minute), now)
	if err != nil || step != RecentInterval || len(across) != 1 || len(across[0].Points) != 180 {
		t.Fatalf("Range(15 min) across the gap = %+v, %v, %v, want every step, from memory and the gap from the database", across, step, err)
	}
	for _, p := range across[0].Points {
		inGap := p.Time > now.Add(-10*time.Minute).Unix() && p.Time < now.Add(-5*time.Minute).Unix()
		if want := map[bool]float64{true: 10, false: 20}[inGap]; p.Value != want {
			t.Errorf("Range(15 min) at %d = %v, want %v", p.Time-now.Unix(), p.Value, want)
		}
	}
	after, step, err := reader.Range(ctx, now.Add(-4*time.Minute), now)
	if err != nil || step != RecentInterval || len(after) != 1 || after[0].Points[0].Value != 20 {
		t.Errorf("Range(4 min) after the gap = %+v, %v, %v, want the readings from memory", after, step, err)
	}
}
