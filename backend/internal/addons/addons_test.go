package addons

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// captureLog sends what slog logs to the returned buffer until the test ends.
func captureLog(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	before := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(before) })
	return buf
}

// syncBuffer is a bytes.Buffer that the add-on's goroutine and the test may
// use at once.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestServeFailsWithoutTheFolder(t *testing.T) {
	t.Setenv("ADDONS_DIR", filepath.Join(t.TempDir(), "missing"))
	read := func(context.Context, time.Time) []metrics.Extra { return nil }
	if err := Serve(context.Background(), "test", read); err == nil {
		t.Error("Serve() without the add-on folder = nil, want an error so the service is restarted")
	}
}

func TestRunLogsAFailedWriteOnceAndWhenItWorksAgain(t *testing.T) {
	log := captureLog(t)
	dir := filepath.Join(t.TempDir(), "addons")
	file := filepath.Join(dir, "test.json")
	reads := make(chan struct{}, 100)
	read := func(context.Context, time.Time) []metrics.Extra {
		// Without blocking, as the test stops taking them after a few.
		select {
		case reads <- struct{}{}:
		default:
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		run(ctx, read, file, time.Millisecond)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	// Several writes fail while the folder is missing.
	for range 5 {
		<-reads
	}
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		if strings.Contains(log.String(), "works again") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the write working again was not logged; log:\n%s", log.String())
		}
	}
	if n := strings.Count(log.String(), "write the add-on's report"); n != 1 {
		t.Errorf("the failed write was logged %d times, want once; log:\n%s", n, log.String())
	}
}

func TestWarnReadLogsOnceAndNotForAMissingFile(t *testing.T) {
	log := captureLog(t)
	path := filepath.Join(t.TempDir(), "energy_uj")
	denied := &fs.PathError{Op: "open", Path: path, Err: fs.ErrPermission}
	WarnRead(path, denied)
	WarnRead(path, denied)
	missing := filepath.Join(t.TempDir(), "missing")
	WarnRead(missing, &fs.PathError{Op: "open", Path: missing, Err: fs.ErrNotExist})
	WarnRead(missing, nil)

	if n := strings.Count(log.String(), "cannot be read"); n != 1 {
		t.Errorf("logged %d times, want once for the denied file; log:\n%s", n, log.String())
	}
	if strings.Contains(log.String(), "missing") {
		t.Errorf("a missing file was logged:\n%s", log.String())
	}
}
