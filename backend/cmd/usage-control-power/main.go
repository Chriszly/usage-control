// Command usage-control-power is the power add-on of usage-control: every
// few seconds it reads how much power the machine draws and writes it to the
// add-on folder, which usage-control shows on its page and keeps in the hub's
// history. It runs as root on Linux, as newer kernels let only root read the
// CPU's energy counters; usage-control itself stays without privileges. On
// Windows, where it reads NVIDIA GPUs only, the installer runs it as the
// service UsageControlPower.
//
// Settings come from environment variables:
//
//	ADDONS_DIR  the add-on folder usage-control reads (default
//	            /run/usage-control-addons, the folder the systemd service
//	            makes; on Windows C:\ProgramData\Usage Control\addons)
//	HOST_SYS    where the host's /sys is, in a container (default /sys)
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/addons/power"
	"github.com/Chriszly/usage-control/backend/internal/metrics"
	"github.com/Chriszly/usage-control/backend/internal/winservice"
)

// interval is how often the power is read, as often as usage-control reads
// the rest.
const interval = 5 * time.Second

func main() {
	// Installed on Windows, the service manager starts the program and tells
	// it when to stop; everywhere else it runs until it is interrupted.
	ranAsService, err := winservice.Run("UsageControlPower", serve)
	if !ranAsService && err == nil {
		err = serve(context.Background())
	}
	if err != nil {
		slog.Error("the power add-on stopped", "error", err)
		os.Exit(1)
	}
}

// serve writes the power to the add-on folder until parent is done or the
// program is interrupted.
func serve(parent context.Context) error {
	dir := os.Getenv("ADDONS_DIR")
	if dir == "" {
		dir = defaultDir()
	}
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	file := filepath.Join(dir, "power.json")
	reader := power.NewReader(power.HostSys())
	slog.Info("writing the power to the add-on folder", "file", file)
	run(ctx, reader, file)
	// Gone with the add-on, so usage-control stops showing old values at once.
	_ = os.Remove(file) //nolint:gosec // the add-on's own file, in the folder its setting names
	return nil
}

// defaultDir is the add-on folder the installers set up.
func defaultDir() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("ProgramData"), "Usage Control", "addons")
	}
	return "/run/usage-control-addons"
}

// run writes a report every interval until ctx is done. A failed write is
// logged once and tried again at the next interval.
func run(ctx context.Context, reader *power.Reader, file string) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	failing := false
	for {
		now := time.Now().UTC()
		report := metrics.AddOnReport{Time: now, Extras: power.Extras(reader.Read(ctx, now))}
		err := metrics.WriteAddOnReport(file, report)
		switch {
		case err != nil && !failing:
			slog.Error("write the power report", "file", file, "error", err)
		case err == nil && failing:
			slog.Info("writing the power report works again", "file", file)
		}
		failing = err != nil
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
