// Command usage-control-power is the power add-on of usage-control: every
// few seconds it reads how much power the machine draws and writes it to the
// add-on folder, which usage-control shows on its page and keeps in the hub's
// history. It runs as root on Linux, as newer kernels let only root read the
// CPU's energy counters; usage-control itself stays without privileges.
//
// Settings come from environment variables:
//
//	ADDONS_DIR  the add-on folder usage-control reads (default
//	            /run/usage-control-addons, the folder the systemd service makes)
//	HOST_SYS    where the host's /sys is, in a container (default /sys)
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/addons/power"
	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

// interval is how often the power is read, as often as usage-control reads
// the rest.
const interval = 5 * time.Second

func main() {
	dir := os.Getenv("ADDONS_DIR")
	if dir == "" {
		dir = "/run/usage-control-addons"
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	file := filepath.Join(dir, "power.json")
	reader := power.NewReader(power.HostSys())
	slog.Info("writing the power to the add-on folder", "file", file)
	run(ctx, reader, file)
	// Gone with the add-on, so usage-control stops showing old values at once.
	_ = os.Remove(file) //nolint:gosec // the add-on's own file, in the folder its setting names
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
