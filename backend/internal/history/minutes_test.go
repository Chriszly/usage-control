package history

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func openTestBuffer(t *testing.T, span time.Duration) *Buffer {
	t.Helper()
	buffer, err := OpenBuffer(context.Background(), filepath.Join(t.TempDir(), "buffer.db"), span)
	if err != nil {
		t.Fatalf("OpenBuffer() error = %v", err)
	}
	t.Cleanup(func() { _ = buffer.Close() })
	return buffer
}

func TestBufferHandsOutItsMinutesAndForgetsTheFetchedOnes(t *testing.T) {
	ctx := context.Background()
	buffer := openTestBuffer(t, time.Hour)
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

func TestBufferDeletesOnlyTheMinutesItHandedOut(t *testing.T) {
	ctx := context.Background()
	buffer := openTestBuffer(t, time.Hour)
	start := time.Unix(1_800_000_000, 0)
	for i := range 4 {
		if err := buffer.Add(ctx, LocalDevice, start.Add(time.Duration(i)*time.Minute), map[string]float64{MetricCPU: float64(i)}); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}

	// Asking after a time no minute was handed out up to deletes nothing.
	if got, _, err := buffer.Since(ctx, start.Add(time.Hour), 10); err != nil || len(got) != 0 {
		t.Fatalf("Since(an hour later) = %+v, %v; want nothing", got, err)
	}
	if got, more, err := buffer.Since(ctx, time.Unix(0, 0), 2); err != nil || !more || len(got) != 2 {
		t.Fatalf("Since(0, 2) = %+v, %v, %v; want the two oldest of all four and more", got, more, err)
	}
	// Once two were handed out, asking after a later time deletes those two only.
	if _, _, err := buffer.Since(ctx, start.Add(time.Hour), 10); err != nil {
		t.Fatalf("Since(an hour later) error = %v", err)
	}
	got, _, err := buffer.Since(ctx, time.Unix(0, 0), 10)
	if err != nil || len(got) != 2 || got[0].Time != start.Unix()+120 {
		t.Errorf("Since(0) = %+v, %v; want the two minutes no one fetched yet", got, err)
	}
}

func TestOpenBufferCreatesOnlyItsTable(t *testing.T) {
	ctx := context.Background()
	buffer := openTestBuffer(t, time.Hour)
	var tables []string
	rows, err := buffer.db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if !reflect.DeepEqual(tables, []string{"buffer"}) {
		t.Errorf("tables = %v, want only buffer", tables)
	}
}

func TestOpenBufferFailsWhereTheFileCannotBeWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "buffer.db")
	if buffer, err := OpenBuffer(context.Background(), path, time.Hour); err == nil {
		_ = buffer.Close()
		t.Error("OpenBuffer() in a folder that does not exist error = nil, want an error")
	}
}

func TestBufferKeepsMinutesForItsSpanOnly(t *testing.T) {
	ctx := context.Background()
	buffer := openTestBuffer(t, time.Hour)
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

func TestAddMinutesLeavesOutTimesStoredAlready(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	hour := time.Unix(1_800_000_000, 0).Truncate(time.Hour)
	if err := store.Add(ctx, LocalDevice, hour, map[string]float64{MetricCPU: 10}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	// The same minute again, as when a hub fetches a minute it stored itself,
	// next to a new one.
	minutes := []Minute{
		{Time: hour.Unix(), Values: map[string]float64{MetricCPU: 90, MetricMemory: 50}},
		{Time: hour.Unix() + 60, Values: map[string]float64{MetricCPU: 30}},
	}
	if err := store.AddMinutes(ctx, LocalDevice, minutes); err != nil {
		t.Fatalf("AddMinutes() error = %v", err)
	}

	minutely, err := store.Range(ctx, LocalDevice, hour, hour.Add(time.Hour), time.Minute)
	want := []Series{
		{Metric: MetricCPU, Points: []Point{{Time: hour.Unix(), Value: 10}, {Time: hour.Unix() + 60, Value: 30}}},
		{Metric: MetricMemory, Points: []Point{{Time: hour.Unix(), Value: 50}}},
	}
	if err != nil || !reflect.DeepEqual(minutely, want) {
		t.Errorf("Range() = %+v, %v; want %+v, the value stored first kept", minutely, err, want)
	}
	hourly, err := store.Range(ctx, LocalDevice, hour, hour.Add(time.Hour), time.Hour)
	want = []Series{
		{Metric: MetricCPU, Points: []Point{{Time: hour.Unix(), Value: 20}}},
		{Metric: MetricMemory, Points: []Point{{Time: hour.Unix(), Value: 50}}},
	}
	if err != nil || !reflect.DeepEqual(hourly, want) {
		t.Errorf("hourly Range() = %+v, %v; want %+v, each minute counted once", hourly, err, want)
	}
}
