package history

import (
	"context"
	"testing"
	"time"
)

func TestPrunerDeletesValuesOlderThanTheRetention(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	now := time.Now().Truncate(time.Second)
	for _, at := range []time.Time{now.AddDate(0, 0, -31), now} {
		if err := store.Add(ctx, LocalDevice, at, map[string]float64{MetricCPU: 1}); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
	}

	(&Pruner{Store: store, Retention: 30 * 24 * time.Hour}).prune(ctx)

	got, err := store.Range(ctx, LocalDevice, now.AddDate(0, 0, -40), now.Add(time.Second), time.Second)
	if err != nil {
		t.Fatalf("Range() error = %v", err)
	}
	if len(got) != 1 || len(got[0].Points) != 1 || got[0].Points[0].Time != now.Unix() {
		t.Errorf("after prune, Range() = %+v, want only the value from now", got)
	}
}
