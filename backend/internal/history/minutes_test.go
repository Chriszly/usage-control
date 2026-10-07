package history

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// hub is the address a hub asks from.
const hub = "192.168.1.10"

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

	first, more, err := buffer.Since(ctx, hub, time.Unix(0, 0), 2, 1000)
	want := []Minute{
		{Time: start.Unix(), Values: map[string]float64{MetricCPU: 0, MetricMemory: 50}},
		{Time: start.Unix() + 60, Values: map[string]float64{MetricCPU: 1, MetricMemory: 50}},
	}
	if err != nil || !more || !reflect.DeepEqual(first, want) {
		t.Fatalf("Since(0, 2) = %+v, %v, %v; want the two oldest minutes and more", first, more, err)
	}
	// Asking after the second tells that the hub has both: they are deleted.
	rest, more, err := buffer.Since(ctx, hub, start.Add(time.Minute), 10, 1000)
	if err != nil || more || len(rest) != 3 || rest[0].Time != start.Unix()+120 {
		t.Fatalf("Since(second, 10) = %+v, %v, %v; want the last three and no more", rest, more, err)
	}
	again, _, err := buffer.Since(ctx, hub, time.Unix(0, 0), 10, 1000)
	if err != nil || len(again) != 3 {
		t.Errorf("Since(0) after the hub had two = %+v, %v; want only the three left", again, err)
	}
	empty, more, err := buffer.Since(ctx, hub, start.Add(4*time.Minute), 10, 1000)
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
	if got, _, err := buffer.Since(ctx, hub, start.Add(time.Hour), 10, 1000); err != nil || len(got) != 0 {
		t.Fatalf("Since(an hour later) = %+v, %v; want nothing", got, err)
	}
	if got, more, err := buffer.Since(ctx, hub, time.Unix(0, 0), 2, 1000); err != nil || !more || len(got) != 2 {
		t.Fatalf("Since(0, 2) = %+v, %v, %v; want the two oldest of all four and more", got, more, err)
	}
	// Once two were handed out, asking after a later time deletes those two only.
	if _, _, err := buffer.Since(ctx, hub, start.Add(time.Hour), 10, 1000); err != nil {
		t.Fatalf("Since(an hour later) error = %v", err)
	}
	got, _, err := buffer.Since(ctx, hub, time.Unix(0, 0), 10, 1000)
	if err != nil || len(got) != 2 || got[0].Time != start.Unix()+120 {
		t.Errorf("Since(0) = %+v, %v; want the two minutes no one fetched yet", got, err)
	}
}

func TestBufferDeletesOnlyTheMinutesEveryHubHas(t *testing.T) {
	ctx := context.Background()
	buffer := openTestBuffer(t, time.Hour)
	start := time.Unix(1_800_000_000, 0)
	for i := range 4 {
		if err := buffer.Add(ctx, LocalDevice, start.Add(time.Duration(i)*time.Minute), map[string]float64{MetricCPU: float64(i)}); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}
	// The hub has the first two.
	if _, _, err := buffer.Since(ctx, hub, time.Unix(0, 0), 2, 1000); err != nil {
		t.Fatalf("Since() error = %v", err)
	}
	if _, _, err := buffer.Since(ctx, hub, start.Add(time.Minute), 10, 1000); err != nil {
		t.Fatalf("Since() error = %v", err)
	}
	// A second hub fetches everything and says it has it.
	const other = "192.168.1.99"
	if _, _, err := buffer.Since(ctx, other, time.Unix(0, 0), 10, 1000); err != nil {
		t.Fatalf("Since() error = %v", err)
	}
	if _, _, err := buffer.Since(ctx, other, start.Add(3*time.Minute), 10, 1000); err != nil {
		t.Fatalf("Since() error = %v", err)
	}

	got, _, err := buffer.Since(ctx, hub, start.Add(time.Minute), 10, 1000)
	if err != nil || len(got) != 2 || got[0].Time != start.Unix()+120 {
		t.Errorf("Since() for the first hub = %+v, %v; want the two minutes it does not have yet", got, err)
	}
}

// bufferedMinutes returns how many minutes the buffer has.
func bufferedMinutes(t *testing.T, buffer *Buffer) int {
	t.Helper()
	var n int
	if err := buffer.db.QueryRowContext(context.Background(), `SELECT COUNT(DISTINCT time) FROM buffer`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestBufferKeepsTheMinutesOfAHubThatIsAway(t *testing.T) {
	ctx := context.Background()
	buffer := openTestBuffer(t, time.Hour)
	start := time.Now().Truncate(time.Minute).Add(-3 * time.Minute)
	for i := range 3 {
		if err := buffer.Add(ctx, LocalDevice, start.Add(time.Duration(i)*time.Minute), map[string]float64{MetricCPU: float64(i)}); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}
	// A hub that last asked longer ago than the span, and had none of them.
	if _, err := buffer.db.ExecContext(ctx, `INSERT INTO buffer_hubs (hub, fetched, sent, asked) VALUES ('192.168.1.99', 0, 0, ?)`, time.Now().Add(-2*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := buffer.Since(ctx, hub, time.Unix(0, 0), 10, 1000); err != nil {
		t.Fatalf("Since() error = %v", err)
	}
	if _, _, err := buffer.Since(ctx, hub, start.Add(2*time.Minute), 10, 1000); err != nil {
		t.Fatalf("Since() error = %v", err)
	}
	if left := bufferedMinutes(t, buffer); left != 3 {
		t.Errorf("%d minutes left, want all 3 for the hub that is away", left)
	}
}

func TestBufferDeletesNothingForARequestNotFromAHub(t *testing.T) {
	ctx := context.Background()
	buffer := openTestBuffer(t, time.Hour)
	start := time.Unix(1_800_000_000, 0)
	for i := range 3 {
		if err := buffer.Add(ctx, LocalDevice, start.Add(time.Duration(i)*time.Minute), map[string]float64{MetricCPU: float64(i)}); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}
	for _, after := range []time.Time{time.Unix(0, 0), start.Add(2 * time.Minute)} {
		if got, _, err := buffer.Since(ctx, "", after, 10, 1000); err != nil || (after.Unix() == 0 && len(got) != 3) {
			t.Fatalf("Since(%v) = %+v, %v; want the minutes", after, got, err)
		}
	}
	var hubs int
	if err := buffer.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM buffer_hubs`).Scan(&hubs); err != nil || hubs != 0 || bufferedMinutes(t, buffer) != 3 {
		t.Errorf("%d hubs kept, %v, %d minutes left; want none kept and all 3 left", hubs, err, bufferedMinutes(t, buffer))
	}
}

func TestBufferKeepsTheHubsThatAskedMostRecently(t *testing.T) {
	ctx := context.Background()
	buffer := openTestBuffer(t, time.Hour)
	for i := range maxBufferHubs + 4 {
		if _, err := buffer.db.ExecContext(ctx, `INSERT INTO buffer_hubs (hub, fetched, sent, asked) VALUES (?, 0, 0, ?)`, fmt.Sprintf("192.168.1.%d", 100+i), 1000+i); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := buffer.Since(ctx, hub, time.Unix(0, 0), 10, 1000); err != nil {
		t.Fatalf("Since() error = %v", err)
	}
	var hubs, oldest int
	if err := buffer.db.QueryRowContext(ctx, `SELECT COUNT(*), MIN(asked) FROM buffer_hubs`).Scan(&hubs, &oldest); err != nil || hubs != maxBufferHubs || oldest != 1000+5 {
		t.Errorf("%d hubs kept, the oldest asked at %d, %v; want %d, the ones that asked most recently", hubs, oldest, err, maxBufferHubs)
	}
}

func TestBufferWritesOnlyWhatChanged(t *testing.T) {
	ctx := context.Background()
	buffer := openTestBuffer(t, time.Hour)
	if err := buffer.Add(ctx, LocalDevice, time.Unix(1_800_000_000, 0), map[string]float64{MetricCPU: 1}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	asked := func() int64 {
		t.Helper()
		var at int64
		if err := buffer.db.QueryRowContext(ctx, `SELECT asked FROM buffer_hubs WHERE hub = ?`, hub).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}
	if _, _, err := buffer.Since(ctx, hub, time.Unix(1_800_000_000, 0), 10, 1000); err != nil {
		t.Fatalf("Since() error = %v", err)
	}
	if _, err := buffer.db.ExecContext(ctx, `UPDATE buffer_hubs SET asked = asked - 30`); err != nil {
		t.Fatal(err)
	}
	before := asked()
	// The same again within a minute changes nothing, so it writes nothing.
	if _, _, err := buffer.Since(ctx, hub, time.Unix(1_800_000_000, 0), 10, 1000); err != nil {
		t.Fatalf("Since() error = %v", err)
	}
	if asked() != before {
		t.Error("the same request within a minute wrote when the hub asked")
	}
	if _, err := buffer.db.ExecContext(ctx, `UPDATE buffer_hubs SET asked = asked - 60`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := buffer.Since(ctx, hub, time.Unix(1_800_000_000, 0), 10, 1000); err != nil {
		t.Fatalf("Since() error = %v", err)
	}
	if asked() <= before {
		t.Error("a request a minute later did not write when the hub asked")
	}
}

func TestBufferAnswersWithAtMostTheValuesAskedFor(t *testing.T) {
	ctx := context.Background()
	buffer := openTestBuffer(t, time.Hour)
	start := time.Unix(1_800_000_000, 0)
	for i := range 3 {
		values := map[string]float64{MetricCPU: float64(i), MetricMemory: 50, MetricSwap: 1}
		if err := buffer.Add(ctx, LocalDevice, start.Add(time.Duration(i)*time.Minute), values); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}
	for _, c := range []struct{ values, want int }{{7, 2}, {6, 2}, {5, 1}, {1, 1}} {
		got, more, err := buffer.Since(ctx, hub, time.Unix(0, 0), 10, c.values)
		if err != nil || len(got) != c.want || !more {
			t.Errorf("Since() of %d values = %d minutes, %v, %v; want %d and more, at least one", c.values, len(got), more, err, c.want)
		}
	}
}

func TestBufferKeepsHowTheExtrasAreDescribed(t *testing.T) {
	ctx := context.Background()
	buffer := openTestBuffer(t, time.Hour)
	info := map[string]ExtraInfo{"extra:power/cpu": {Title: "Power", Label: "CPU", Unit: metrics.UnitWatts}}
	if err := buffer.SetExtraInfo(ctx, LocalDevice, info, time.Now()); err != nil {
		t.Fatalf("SetExtraInfo() error = %v", err)
	}
	got, err := buffer.ExtraInfo(ctx)
	if err != nil || !reflect.DeepEqual(got, info) {
		t.Errorf("ExtraInfo() = %+v, %v; want %+v", got, err, info)
	}
	// Like the minutes, for the span at most.
	if err := buffer.Add(ctx, LocalDevice, time.Now().Add(2*time.Hour), map[string]float64{MetricCPU: 1}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if got, err := buffer.ExtraInfo(ctx); err != nil || len(got) != 0 {
		t.Errorf("ExtraInfo() after the span = %+v, %v; want none", got, err)
	}
}

func TestRecorderWritesHowTheExtrasAreDescribedToABuffer(t *testing.T) {
	ctx := context.Background()
	buffer := openTestBuffer(t, time.Hour)
	value := 12.5
	recorder := &Recorder{
		Store:  buffer,
		Recent: &Recent{},
		Collector: &sequenceCollector{[]metrics.Snapshot{{Time: time.Now(), Extras: []metrics.Extra{{
			ID: "power", Title: "Power",
			Items: []metrics.ExtraItem{{ID: "cpu", Label: "CPU", Unit: metrics.UnitWatts, Value: &value, History: true}},
		}}}}},
		Device:     LocalDevice,
		MaxEntries: DefaultMaxEntries,
	}
	recorder.read(ctx)
	if got, err := buffer.ExtraInfo(ctx); err != nil || got["extra:power/cpu"].Label != "CPU" {
		t.Errorf("ExtraInfo() = %+v, %v; want the power extra described", got, err)
	}
}

func TestOpenBufferCreatesOnlyItsTables(t *testing.T) {
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
	if want := []string{"buffer", "buffer_extra_info", "buffer_hubs"}; !reflect.DeepEqual(tables, want) {
		t.Errorf("tables = %v, want only %v", tables, want)
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
	got, _, err := buffer.Since(ctx, hub, time.Unix(0, 0), 10, 1000)
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
		got, more, err := reader.Since(ctx, hub, start, 10, 1000)
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
