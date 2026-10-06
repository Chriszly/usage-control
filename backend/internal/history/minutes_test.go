package history

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestBufferHandsOutItsMinutesAndForgetsTheFetchedOnes(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	buffer, err := NewBuffer(ctx, store.DB(), time.Hour)
	if err != nil {
		t.Fatalf("NewBuffer() error = %v", err)
	}
	start := time.Unix(1_800_000_000, 0)
	for i := range 5 {
		values := map[string]float64{MetricCPU: float64(i), MetricMemory: 50}
		if err := buffer.Add(ctx, LocalDevice, start.Add(time.Duration(i)*time.Minute), values); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}

	first, more, err := buffer.Since(ctx, time.Unix(0, 0), 2)
	want := []Minute{
		{Time: start.Unix(), Values: map[string]float64{MetricCPU: 0, MetricMemory: 50}},
		{Time: start.Unix() + 60, Values: map[string]float64{MetricCPU: 1, MetricMemory: 50}},
	}
	if err != nil || !more || !reflect.DeepEqual(first, want) {
		t.Fatalf("Since(0, 2) = %+v, %v, %v; want the two oldest minutes and more", first, more, err)
	}
	// Asking after the second tells that the hub has both: they are deleted.
	rest, more, err := buffer.Since(ctx, start.Add(time.Minute), 10)
	if err != nil || more || len(rest) != 3 || rest[0].Time != start.Unix()+120 {
		t.Fatalf("Since(second, 10) = %+v, %v, %v; want the last three and no more", rest, more, err)
	}
	again, _, err := buffer.Since(ctx, time.Unix(0, 0), 10)
	if err != nil || len(again) != 3 {
		t.Errorf("Since(0) after the hub had two = %+v, %v; want only the three left", again, err)
	}
	empty, more, err := buffer.Since(ctx, start.Add(4*time.Minute), 10)
	if err != nil || more || empty == nil || len(empty) != 0 {
		t.Errorf("Since(last) = %#v, %v, %v; want an empty list", empty, more, err)
	}
}

func TestBufferKeepsMinutesForItsSpanOnly(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	buffer, err := NewBuffer(ctx, store.DB(), time.Hour)
	if err != nil {
		t.Fatalf("NewBuffer() error = %v", err)
	}
	start := time.Unix(1_800_000_000, 0)
	for _, at := range []time.Time{start, start.Add(30 * time.Minute), start.Add(61 * time.Minute)} {
		if err := buffer.Add(ctx, LocalDevice, at, map[string]float64{MetricCPU: 1}); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}
	got, _, err := buffer.Since(ctx, time.Unix(0, 0), 10)
	if err != nil || len(got) != 2 || got[0].Time != start.Add(30*time.Minute).Unix() {
		t.Errorf("Since(0) = %+v, %v; want the two minutes of the last hour", got, err)
	}
}

func TestReaderHandsOutTheStoredMinutesAndKeepsThem(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	start := time.Unix(1_800_000_000, 0)
	for i := range 3 {
		if err := store.Add(ctx, LocalDevice, start.Add(time.Duration(i)*time.Minute), map[string]float64{MetricCPU: float64(i)}); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}
	if err := store.Add(ctx, "living-room-pi", start.Add(3*time.Minute), map[string]float64{MetricCPU: 9}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	reader := Reader{Store: store, Recent: &Recent{}, Device: LocalDevice}

	for range 2 {
		got, more, err := reader.Since(ctx, start, 10)
		want := []Minute{
			{Time: start.Unix() + 60, Values: map[string]float64{MetricCPU: 1}},
			{Time: start.Unix() + 120, Values: map[string]float64{MetricCPU: 2}},
		}
		if err != nil || more || !reflect.DeepEqual(got, want) {
			t.Errorf("Since(first) = %+v, %v, %v; want this device's two later minutes, every time", got, more, err)
		}
	}
}

func TestAddMinutesStoresThemLikeAddEach(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	hour := time.Unix(1_800_000_000, 0).Truncate(time.Hour)
	minutes := []Minute{
		{Time: hour.Unix(), Values: map[string]float64{MetricCPU: 10}},
		{Time: hour.Unix() + 60, Values: map[string]float64{MetricCPU: 30}},
		{Time: hour.Unix() + 3600, Values: map[string]float64{MetricCPU: 50}},
	}
	if err := store.AddMinutes(ctx, LocalDevice, minutes); err != nil {
		t.Fatalf("AddMinutes() error = %v", err)
	}
	got, err := store.Range(ctx, LocalDevice, hour, hour.Add(2*time.Hour), time.Hour)
	want := []Series{{Metric: MetricCPU, Points: []Point{{Time: hour.Unix(), Value: 20}, {Time: hour.Unix() + 3600, Value: 50}}}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("hourly Range() = %+v, %v; want %+v", got, err, want)
	}
}
