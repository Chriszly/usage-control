package addons

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

func TestServeWritesTheReportAndRemovesItAtTheEnd(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ADDONS_DIR", dir)
	file := filepath.Join(dir, "test.json")
	value := 1.5
	monotonic := make(chan bool, 1)
	read := func(_ context.Context, now time.Time) []metrics.Extra {
		// Round(0) drops the monotonic clock reading, so it differs when now has one.
		select {
		case monotonic <- now != now.Round(0):
		default:
		}
		return []metrics.Extra{{ID: "test", Title: "Test", Items: []metrics.ExtraItem{{ID: "a", Label: "A", Unit: metrics.UnitNumber, Value: &value}}}}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, "test", read) }()

	var report metrics.AddOnReport
	for deadline := time.Now().Add(5 * time.Second); ; {
		data, err := os.ReadFile(file) //nolint:gosec // a file in the test's own temporary folder
		if err == nil && json.Unmarshal(data, &report) == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no report was written")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(report.Extras) != 1 || report.Extras[0].Items[0].ID != "a" || report.Time.IsZero() {
		t.Errorf("report = %+v", report)
	}
	if !<-monotonic {
		t.Error("read got a time without the monotonic clock reading, so rates jump when the clock is set")
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("the report is still there: %v", err)
	}
}
