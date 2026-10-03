// Command usage-control serves a website that shows the usage of the machine
// it runs on.
//
// Settings come from environment variables:
//
//	LISTEN_ADDR     address to listen on (default ":8080")
//	DISK_PATHS      comma-separated paths whose disk usage is shown (default "/",
//	                or the system drive such as "C:\" on Windows)
//	DATABASE_PATH   SQLite file the history is kept in (default "usage-control.db")
//	RETENTION_DAYS  days of history to keep; older values are deleted (default 30)
//	DEVICE_NAME     how the page names this device (default "This device")
//	HUB_DEVICES     other devices to collect from, which turns on hub mode:
//	                comma-separated name=host:port entries (default none)
//	DATA_ONLY       true to serve only the usage data for a hub, without the
//	                website and history (default false)
//	RESET_PASSWORD  true to delete the password for adding and removing
//	                devices on the page, if it is forgotten (default false)
//	UPDATE_CHECK    false to stop asking GitHub once a day whether a newer
//	                release exists, which the page then tells (default true;
//	                only releases check, and never with DATA_ONLY)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/history"
	"github.com/Chriszly/usage-control/backend/internal/hub"
	"github.com/Chriszly/usage-control/backend/internal/metrics"
	"github.com/Chriszly/usage-control/backend/internal/password"
	"github.com/Chriszly/usage-control/backend/internal/server"
	"github.com/Chriszly/usage-control/backend/internal/update"
	"github.com/Chriszly/usage-control/backend/internal/version"
	"github.com/Chriszly/usage-control/backend/internal/web"
)

func main() {
	// Installed on Windows, the service manager starts the program and tells
	// it when to stop; everywhere else it runs until it is interrupted.
	ranAsService, err := runAsService(run)
	if !ranAsService && err == nil {
		err = run(context.Background())
	}
	if err != nil {
		slog.Error("usage-control stopped", "error", err)
		os.Exit(1)
	}
}

// run serves the website, or with DATA_ONLY only the usage data, until parent is done or the program is interrupted.
func run(parent context.Context) error {
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	retention, err := retentionDays()
	if err != nil {
		return err
	}
	remotes, err := hub.ParseDevices(os.Getenv("HUB_DEVICES"))
	if err != nil {
		return fmt.Errorf("check HUB_DEVICES: %w", err)
	}

	dataOnly, err := dataOnly()
	if err != nil {
		return err
	}
	if dataOnly && len(remotes) > 0 {
		return errors.New("HUB_DEVICES is set, but DATA_ONLY turns off the website that would show them; unset one of the two")
	}

	collector, err := metrics.NewCollector(context.Background(), diskPaths())
	if err != nil {
		return fmt.Errorf("check DISK_PATHS: %w; mount each path read-only in compose.yaml", err)
	}

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// With DATA_ONLY, a hub collects the usage and keeps the history, so this
	// device keeps none and only answers the hub.
	handler := server.NewDataOnly(collector)
	if dataOnly {
		slog.Info("serving only the usage data, for a hub; the website is turned off")
	} else {
		databasePath := os.Getenv("DATABASE_PATH")
		if databasePath == "" {
			databasePath = "usage-control.db"
		}
		store, err := history.Open(context.Background(), databasePath)
		if err != nil {
			return fmt.Errorf("open the history database %s: %w; set DATABASE_PATH to a writable file", databasePath, err)
		}
		defer func() { _ = store.Close() }()

		site, waitForRecorders, err := withHistory(ctx, collector, store, remotes, retention)
		if err != nil {
			return err
		}
		// The recorders stop before the database is closed.
		defer func() {
			stop()
			waitForRecorders()
		}()
		handler = server.New(site)
	}

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		slog.Info("usage-control listening", "addr", addr)
		serveErr <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// withHistory returns the website's devices with their history: this device
// and every device the hub collects from, from HUB_DEVICES or added on the
// page. Each gets a recorder that reads its usage into the history until ctx
// is done; the returned function waits until they have stopped.
func withHistory(ctx context.Context, collector *metrics.Collector, store *history.Store, fixed []hub.Device, retention time.Duration) (server.Site, func(), error) {
	// The recorder measures with its own collector, so CPU usage and network
	// speed in the history are averages over its own interval.
	recorderCollector, err := metrics.NewCollector(ctx, diskPaths())
	if err != nil {
		return server.Site{}, nil, err
	}

	devicesPassword, err := password.Open(ctx, store.DB())
	if err != nil {
		return server.Site{}, nil, err
	}
	reset, err := boolSetting("RESET_PASSWORD")
	if err != nil {
		return server.Site{}, nil, err
	}
	if reset {
		if err := devicesPassword.Reset(ctx); err != nil {
			return server.Site{}, nil, err
		}
		slog.Warn("RESET_PASSWORD deleted the password for changing devices; the next change chooses a new one. Unset RESET_PASSWORD again.")
	}

	others, err := hub.New(ctx, store, retention, fixed)
	if err != nil {
		return server.Site{}, nil, err
	}

	recent := &history.Recent{}
	recorder := &history.Recorder{Store: store, Recent: recent, Collector: recorderCollector, Retention: retention}
	var recording sync.WaitGroup
	recording.Go(func() { recorder.Run(ctx) })

	site := server.Site{
		Devices: hubDevices{
			local: server.Device{
				ID:      hub.LocalID,
				Name:    strings.TrimSpace(os.Getenv("DEVICE_NAME")),
				Metrics: collector,
				History: history.Reader{Store: store, Recent: recent},
			},
			hub: others,
		},
		Hub:       others,
		Password:  devicesPassword,
		Retention: retention,
		Files:     web.Files(),
		Update:    func() update.Status { return update.Status{Current: version.Version} },
	}
	checkUpdates, err := boolSettingOr("UPDATE_CHECK", true)
	if err != nil {
		return server.Site{}, nil, err
	}
	if checker := update.NewChecker(version.Version); checker != nil && checkUpdates {
		site.Update = checker.Status
		recording.Go(func() { checker.Run(ctx) })
	}
	wait := func() {
		recording.Wait()
		others.Wait()
	}
	return site, wait, nil
}

// hubDevices lists this device and the devices the hub collects from.
type hubDevices struct {
	local server.Device
	hub   *hub.Hub
}

func (d hubDevices) List() []server.Device {
	devices := []server.Device{d.local}
	for _, remote := range d.hub.Remotes() {
		since, unreachable := remote.Agent.Unreachable()
		var unreachableSince *time.Time
		if unreachable && !since.IsZero() {
			unreachableSince = &since
		}
		devices = append(devices, server.Device{
			ID:               remote.ID,
			Name:             remote.Name,
			Address:          remote.Address,
			Removable:        !remote.Fixed,
			Unreachable:      unreachable,
			UnreachableSince: unreachableSince,
			Metrics:          remote.Agent.Latest(),
			History:          remote.Reader,
			Availability:     remote,
		})
	}
	return devices
}

// dataOnly reports whether DATA_ONLY turns the website off (default false).
func dataOnly() (bool, error) {
	return boolSetting("DATA_ONLY")
}

// boolSetting reads a setting that is true or false (default false).
func boolSetting(name string) (bool, error) {
	return boolSettingOr(name, false)
}

// boolSettingOr reads a setting that is true or false, with a default for
// when it is not set.
func boolSettingOr(name string, fallback bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	on, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s is %q; set it to true or false", name, value)
	}
	return on, nil
}

// diskPaths returns the paths from DISK_PATHS, or the system disk when it is
// not set.
func diskPaths() []string {
	value := os.Getenv("DISK_PATHS")
	if strings.TrimSpace(value) == "" {
		return []string{systemDisk()}
	}
	var paths []string
	for _, path := range strings.Split(value, ",") {
		if path = strings.TrimSpace(path); path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

// systemDisk returns the root of the disk the operating system is installed
// on: / on Linux and macOS, and the system drive, usually C:\, on Windows.
func systemDisk() string {
	if runtime.GOOS == "windows" {
		return os.Getenv("SystemDrive") + `\`
	}
	return "/"
}

// retentionDays returns how long the history is kept, from RETENTION_DAYS, or
// 30 days when it is not set.
func retentionDays() (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv("RETENTION_DAYS"))
	if value == "" {
		return 30 * 24 * time.Hour, nil
	}
	days, err := strconv.Atoi(value)
	if err != nil || days < 1 || days > 3650 {
		return 0, fmt.Errorf("RETENTION_DAYS is %q; set it to a whole number of days from 1 to 3650", value)
	}
	return time.Duration(days) * 24 * time.Hour, nil
}
