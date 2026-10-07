package history

import (
	"bytes"
	"context"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

func number(v float64) *float64 { return &v }

var pressure = metrics.Extra{
	ID:     "pressure",
	Title:  "Pressure",
	Titles: map[string]string{"de": "Druck"},
	Items: []metrics.ExtraItem{
		{ID: "cpu", Label: "CPU", Unit: metrics.UnitPercent, Value: number(4), History: true},
		{ID: "live", Label: "Only live", Unit: metrics.UnitNumber, Value: number(7)},
		{ID: "kernel", Label: "Kernel", Unit: metrics.UnitText, Text: "6.12"},
	},
}

func TestValuesKeepsTheExtrasThatAskForHistory(t *testing.T) {
	got, _, _ := values(metrics.Snapshot{Extras: []metrics.Extra{pressure}}, DefaultMaxEntries)

	want := map[string]float64{MetricCPU: 0, MetricMemory: 0, "extra:pressure/cpu": 4}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("values() = %v, want %v", got, want)
	}
}

func TestRecorderStoresHowTheExtrasAreDescribed(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Now().Truncate(time.Second)
	recorder := &Recorder{
		Store:      store,
		Recent:     &Recent{},
		Collector:  &sequenceCollector{[]metrics.Snapshot{{Time: now, Extras: []metrics.Extra{pressure}}}},
		Device:     "nas",
		MaxEntries: DefaultMaxEntries,
	}

	recorder.read(ctx)

	got, err := store.ExtraInfo(ctx, "nas")
	if err != nil {
		t.Fatalf("ExtraInfo() error = %v", err)
	}
	want := map[string]ExtraInfo{"extra:pressure/cpu": {
		Title: "Pressure", Titles: map[string]string{"de": "Druck"}, Label: "CPU", Unit: metrics.UnitPercent,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExtraInfo() = %+v, want %+v", got, want)
	}
}

func TestExtraInfoWriterOnlyWritesChanges(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Now().Truncate(time.Second)
	info := map[string]ExtraInfo{"extra:a/b": {Title: "A", Label: "B", Unit: metrics.UnitNumber}}
	var writer extraInfoWriter

	if err := writer.write(ctx, store, "nas", info, now); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteDevice(ctx, "nas"); err != nil {
		t.Fatal(err)
	}
	if err := writer.write(ctx, store, "nas", info, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.ExtraInfo(ctx, "nas"); len(got) != 0 {
		t.Errorf("ExtraInfo() = %v after the same descriptions, want them not written again", got)
	}
	if err := writer.write(ctx, store, "nas", info, now.Add(extraInfoRefresh)); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.ExtraInfo(ctx, "nas"); len(got) != 1 {
		t.Errorf("ExtraInfo() = %v after extraInfoRefresh, want them written again", got)
	}
}

func TestExtraInfoLeavesOutABrokenDescription(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	info := map[string]ExtraInfo{"extra:a/b": {Title: "A", Label: "B", Unit: metrics.UnitNumber}}
	if err := store.SetExtraInfo(ctx, "nas", info, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO extra_info (device, metric, info, written) VALUES ('nas', 'extra:a/c', '{', 0)`); err != nil {
		t.Fatal(err)
	}

	got, err := store.ExtraInfo(ctx, "nas")

	if err != nil || len(got) != 1 || got["extra:a/b"].Label != "B" {
		t.Errorf("ExtraInfo() = %v, %v, want the one description that can be read", got, err)
	}
}

func TestDeleteBeforeDeletesTheDescriptionsOfExtrasWithoutValues(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Now().Truncate(time.Hour)
	old := now.Add(-48 * time.Hour)
	info := map[string]ExtraInfo{"extra:a/kept": {Label: "Kept"}, "extra:a/gone": {Label: "Gone"}, "extra:a/new": {Label: "New"}}
	if err := store.SetExtraInfo(ctx, "nas", info, old); err != nil {
		t.Fatal(err)
	}
	if err := store.SetExtraInfo(ctx, "nas", map[string]ExtraInfo{"extra:a/new": {Label: "New"}}, now); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(ctx, "nas", old, map[string]float64{"extra:a/kept": 1, "extra:a/gone": 2}); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(ctx, "nas", now, map[string]float64{"extra:a/kept": 3}); err != nil {
		t.Fatal(err)
	}

	if _, err := store.DeleteBefore(ctx, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	got, err := store.ExtraInfo(ctx, "nas")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["extra:a/gone"]; ok || len(got) != 2 {
		t.Errorf("ExtraInfo() = %v, want kept (has values) and new (written recently)", got)
	}
}

func TestABrokenDescriptionIsLoggedOnce(t *testing.T) {
	var logged bytes.Buffer
	defer slog.SetDefault(slog.Default())
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	ctx := context.Background()
	store := openTestStore(t)
	for _, metric := range []string{"extra:a/broken", "extra:a/also-broken"} {
		if _, err := store.db.Exec(`INSERT INTO extra_info (device, metric, info, written) VALUES ('nas', ?, '{', 0)`, metric); err != nil {
			t.Fatal(err)
		}
	}

	for range 3 {
		if got, err := store.ExtraInfo(ctx, "nas"); err != nil || len(got) != 0 {
			t.Fatalf("ExtraInfo() = %v, %v; want nothing", got, err)
		}
	}

	if got := strings.Count(logged.String(), "read the description of an extra"); got != 2 {
		t.Errorf("logged %d times, want once per broken description:\n%s", got, logged.String())
	}

	// One that is fixed and breaks again is logged again.
	for _, info := range []string{`{"title":"A"}`, `{`} {
		if _, err := store.db.Exec(`UPDATE extra_info SET info = ? WHERE metric = 'extra:a/broken'`, info); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ExtraInfo(ctx, "nas"); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Count(logged.String(), "read the description of an extra"); got != 3 {
		t.Errorf("logged %d times, want once more after the description was fixed and broke again", got)
	}

	if err := store.DeleteDevice(ctx, "nas"); err != nil {
		t.Fatal(err)
	}
	store.brokenInfo.Range(func(key, _ any) bool {
		t.Errorf("broken description %v kept after its device was deleted", key)
		return true
	})
}
