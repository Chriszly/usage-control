// Package addons runs an add-on of usage-control: a separate program that
// every few seconds reads values usage-control itself does not and writes
// them as extras to the add-on folder, which usage-control shows on its page
// and a hub keeps in its history. Each add-on is a package below this one
// with its own command in cmd/usage-control-<name>.
package addons

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
	"github.com/Chriszly/usage-control/backend/internal/winservice"
)

// Interval is how often an add-on reads, as often as usage-control reads the
// rest.
const Interval = 5 * time.Second

// Read returns an add-on's extras at now. now keeps Go's monotonic clock
// reading, so the time between two reads, which rates are worked out over,
// does not jump when the wall clock is set.
type Read func(ctx context.Context, now time.Time) []metrics.Extra

// Main runs the add-on name, which reads with newRead: as the Windows service
// service when the service manager starts it, and everywhere else until it is
// interrupted. It writes ADDONS_DIR/<name>.json every Interval and exits the
// program when it fails. newRead is called once, when the add-on starts.
func Main(name, service string, newRead func() Read) {
	read := sync.OnceValue(newRead)
	serve := func(ctx context.Context) error { return Serve(ctx, name, read()) }
	ranAsService, err := winservice.Run(service, serve)
	if !ranAsService && err == nil {
		err = serve(context.Background())
	}
	if err != nil {
		slog.Error("the add-on stopped", "add-on", name, "error", err)
		os.Exit(1)
	}
}

// Serve writes what read returns to the add-on folder until parent is done or
// the program is interrupted, then removes the file, so usage-control stops
// showing old values at once.
func Serve(parent context.Context, name string, read Read) error {
	dir := os.Getenv("ADDONS_DIR")
	if dir == "" {
		dir = DefaultDir()
	}
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	file := filepath.Join(dir, name+".json")
	slog.Info("writing to the add-on folder", "file", file)
	run(ctx, read, file)
	_ = os.Remove(file) //nolint:gosec // the add-on's own file, in the folder its setting names
	return nil
}

// DefaultDir is the add-on folder the installers set up.
func DefaultDir() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("ProgramData"), "Usage Control", "addons")
	}
	return "/run/usage-control-addons"
}

// run writes a report every Interval until ctx is done. A failed write is
// logged once and tried again at the next interval.
func run(ctx context.Context, read Read, file string) {
	ticker := time.NewTicker(Interval)
	defer ticker.Stop()
	failing := false
	for {
		// time.Now, not time.Now().UTC(), which drops the monotonic reading.
		now := time.Now()
		err := metrics.WriteAddOnReport(file, metrics.AddOnReport{Time: now.UTC(), Extras: read(ctx, now)})
		switch {
		case err != nil && !failing:
			slog.Error("write the add-on's report", "file", file, "error", err)
		case err == nil && failing:
			slog.Info("writing the add-on's report works again", "file", file)
		}
		failing = err != nil
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
