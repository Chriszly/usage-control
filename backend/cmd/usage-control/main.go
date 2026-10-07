// Command usage-control serves a website that shows the usage of the machine
// it runs on.
//
// Settings come from environment variables:
//
//	LISTEN_ADDR     address to listen on (default ":9393")
//	PUBLIC_PORT     port the page is reachable on from the network, when it is
//	                not the one in LISTEN_ADDR (set by compose.yaml)
//	DISK_PATHS      comma-separated paths whose disk usage is shown (default "/",
//	                or the system drive such as "C:\" on Windows)
//	DATABASE_PATH   SQLite file the history is kept in, or with DATA_ONLY the
//	                minutes kept for a hub (default "usage-control.db")
//	RETENTION_DAYS  days of history to keep; older values are deleted (default 30)
//	HISTORY_MAX_ENTRIES  how many disks, temperature sensors, network cards and
//	                GPUs each the history keeps per device (default 64)
//	DEVICE_NAME     how the page names this device (default "Host Hub")
//	HUB_DEVICES     other devices to collect from, which turns on hub mode:
//	                comma-separated name=host:port entries (default none)
//	DATA_ONLY       true to serve only the usage data for a hub, without the
//	                website and history (default false)
//	BUFFER_HOURS    with DATA_ONLY, how many hours of minutes this device keeps
//	                for a hub that cannot reach it, from 1 to 168; the hub
//	                fetches them once it can, and they are deleted then
//	                (default 24)
//	RESET_PASSWORD  true to delete the password for adding and removing
//	                devices on the page, if it is forgotten (default false)
//	UPDATE_CHECK    false to stop asking GitHub once a day whether a newer
//	                release exists, which the page then tells (default true;
//	                only releases check, and never with DATA_ONLY)
//	ADDONS_DIR      folder the installed add-ons write their reports to, which
//	                are shown as extras (default none: no add-ons)
//	ALLOWED_HOSTS   comma-separated names this device answers to besides its
//	                IP addresses, localhost, its hostname and .local names,
//	                such as a name from the router's DNS (default none)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
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
	"github.com/Chriszly/usage-control/backend/internal/winservice"
)

func main() {
	// Installed on Windows, the service manager starts the program and tells
	// it when to stop; everywhere else it runs until it is interrupted. A
	// service has no console, so its warnings go to the event log too.
	winservice.LogWarnings("UsageControl")
	ranAsService, err := winservice.Run("UsageControl", run)
	if !ranAsService && err == nil {
		// Only outside the service manager: Go turns a user logging off
		// Windows into SIGTERM, which would stop the service for good.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		err = run(ctx)
		stop()
	}
	if err != nil {
		// winservice.Run has written a service's error to the event log already.
		if !ranAsService {
			slog.Error("usage-control stopped", "error", err)
		}
		os.Exit(1)
	}
}

// run serves the website, or with DATA_ONLY only the usage data, until ctx is done.
func run(ctx context.Context) error {
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":9393"
	}

	retention, err := retentionDays()
	if err != nil {
		return err
	}
	port, err := pagePort(addr)
	if err != nil {
		return err
	}
	historyEntries, err := historyMaxEntries()
	if err != nil {
		return err
	}
	remotes, err := hub.ParseDevices(os.Getenv("HUB_DEVICES"))
	if err != nil {
		return fmt.Errorf("check HUB_DEVICES: %w", err)
	}

	dataOnly, err := boolSettingOr("DATA_ONLY", false)
	if err != nil {
		return err
	}
	if dataOnly && len(remotes) > 0 {
		return errors.New("HUB_DEVICES is set, but DATA_ONLY turns off the website that would show them; unset one of the two")
	}

	bufferSpan, err := bufferHours()
	if err != nil {
		return err
	}

	// Listening comes before the rest of the setup: while another program
	// holds the port, the Windows service tries again every 10 seconds, and
	// each try would otherwise open the database and the readers anew.
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()

	collector, err := metrics.NewCollector(context.Background(), diskPaths())
	if err != nil {
		return fmt.Errorf("check DISK_PATHS: %w; mount each path read-only in compose.yaml", err)
	}

	// stop ends the recorders before the database is closed.
	ctx, stop := context.WithCancel(ctx)
	defer stop()

	collector.Name = ownName()
	if dir := os.Getenv("ADDONS_DIR"); dir != "" {
		collector.AddOns = &metrics.AddOns{Dir: dir, MaxEntries: historyEntries}
		slog.Info("showing the values of the add-ons", "folder", dir)
	}

	// One sampler reads the usage for every page, hub and the recorder.
	sampler := metrics.NewSampler(collector)

	databasePath := os.Getenv("DATABASE_PATH")
	if databasePath == "" {
		databasePath = "usage-control.db"
	}

	// With DATA_ONLY, a hub collects the usage and keeps the history, so this
	// device keeps only the minutes the hub has not fetched yet.
	var handler http.Handler
	var waitForRecorders func()
	if dataOnly {
		minutes, wait := withBuffer(ctx, sampler, databasePath, bufferSpan, historyEntries)
		waitForRecorders = wait
		handler = server.NewDataOnly(sampler, minutes, listSetting("ALLOWED_HOSTS"))
		slog.Info("serving only the usage data, for a hub; the website is turned off", "bufferHours", int(bufferSpan/time.Hour))
	} else {
		store, err := history.Open(context.Background(), databasePath)
		if err != nil {
			return fmt.Errorf("open the history database %s: %w; set DATABASE_PATH to a writable file", databasePath, err)
		}
		defer func() { _ = store.Close() }()
		site, wait, err := withHistory(ctx, sampler, store, remotes, retention, historyEntries, port)
		if err != nil {
			return err
		}
		waitForRecorders = wait
		handler = server.New(site)
	}
	// The recorders stop before the database is closed.
	defer func() {
		stop()
		waitForRecorders()
	}()

	// The page gives up on an answer after ANSWER_TIMEOUT_MS (15 s,
	// frontend/src/app/connection/connection.ts); keep WriteTimeout below it.
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
		serveErr <- httpServer.Serve(listener)
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
// is done, and one pruner deletes what is older than retention; the returned
// function waits until they have stopped. Of each device's disks, sensors,
// network cards and GPUs, the first historyEntries are kept.
func withHistory(ctx context.Context, sampler *metrics.Sampler, store *history.Store, fixed []hub.Device, retention time.Duration, historyEntries int, pagePort string) (server.Site, func(), error) {
	devicesPassword, err := password.Open(ctx, store.DB())
	if err != nil {
		return server.Site{}, nil, err
	}
	reset, err := boolSettingOr("RESET_PASSWORD", false)
	if err != nil {
		return server.Site{}, nil, err
	}
	if reset {
		if err := devicesPassword.Reset(ctx); err != nil {
			return server.Site{}, nil, err
		}
		slog.Warn("RESET_PASSWORD deleted the password for changing devices; the next change chooses a new one. Unset RESET_PASSWORD again.")
	}

	others, err := hub.New(ctx, store, fixed, historyEntries, pagePort)
	if err != nil {
		return server.Site{}, nil, err
	}

	recent := &history.Recent{}
	recorder := &history.Recorder{Store: store, Recent: recent, Collector: sampler.Reusing(reuseFor), Device: history.LocalDevice, MaxEntries: historyEntries}
	pruner := &history.Pruner{Store: store, Retention: retention}
	var recording sync.WaitGroup
	recording.Go(func() { recorder.Run(ctx) })
	recording.Go(func() { pruner.Run(ctx) })

	site := server.Site{
		Devices: hubDevices{
			local: server.Device{
				ID:      hub.LocalID,
				Name:    strings.TrimSpace(os.Getenv("DEVICE_NAME")),
				Metrics: sampler,
				History: history.Reader{Store: store, Recent: recent, Device: history.LocalDevice},
			},
			hub: others,
		},
		Hub:       others,
		Password:  devicesPassword,
		Retention: retention,
		Files:     web.Files(),
		Update:    func() update.Status { return update.Status{Current: version.Version} },

		AllowedHosts: listSetting("ALLOWED_HOSTS"),
		Minutes:      history.Reader{Store: store, Recent: recent, Device: history.LocalDevice},
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

// reuseFor is how old a reading the recorder takes from a hub or page that
// asked meanwhile, instead of reading the machine again: less than the time
// between its own readings, so it never takes the same one twice.
const reuseFor = history.RecentInterval - time.Second

// withBuffer keeps this device's minutes for a hub that cannot reach it, for
// up to span, in a buffer in the database at path, until ctx is done; the
// returned function waits until the recorder has stopped and closes the
// database. Of its disks, sensors, network cards and GPUs, the first
// historyEntries are kept. When the database cannot be opened, the device
// still serves its usage, without minutes: the hub then stores the average of
// its own readings, as for a device from before they were kept.
func withBuffer(ctx context.Context, sampler *metrics.Sampler, path string, span time.Duration, historyEntries int) (server.MinuteSource, func()) {
	buffer, err := history.OpenBuffer(ctx, path, span)
	if err != nil {
		slog.Warn("could not open the database for the usage kept for a hub, so a hub that cannot reach this device has a gap in its history; set DATABASE_PATH to a writable file",
			"path", path, "error", err)
		return nil, func() {}
	}
	recorder := &history.Recorder{
		Store: buffer,
		// Only the readings until their average is kept are needed.
		Recent:     &history.Recent{Span: 2 * history.SampleInterval},
		Collector:  sampler.Reusing(reuseFor),
		Device:     history.LocalDevice,
		MaxEntries: historyEntries,
	}
	var recording sync.WaitGroup
	recording.Go(func() { recorder.Run(ctx) })
	wait := func() {
		recording.Wait()
		_ = buffer.Close()
	}
	return buffer, wait
}

// hubDevices lists this device and the devices the hub collects from.
type hubDevices struct {
	local server.Device
	hub   *hub.Hub
}

func (d hubDevices) List() []server.Device {
	devices := []server.Device{d.local}
	for _, remote := range d.hub.Remotes() {
		since, unreachable := remote.Unreachable()
		var unreachableSince *time.Time
		if unreachable && !since.IsZero() {
			unreachableSince = &since
		}
		devices = append(devices, server.Device{
			ID:               remote.ID,
			Name:             remote.Name,
			Address:          remote.Address,
			Kind:             remote.Kind(),
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

// ownName returns what this device calls itself, which a hub offers as the
// name when it is added there: DEVICE_NAME, or else the machine's hostname.
// In a container, where HOST_PROC is set, the hostname is the container's,
// so there it is only DEVICE_NAME.
func ownName() string {
	if name := strings.TrimSpace(os.Getenv("DEVICE_NAME")); name != "" {
		return name
	}
	if os.Getenv("HOST_PROC") != "" {
		return ""
	}
	hostname, _ := os.Hostname()
	return hostname
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
	paths := listSetting("DISK_PATHS")
	if len(paths) == 0 {
		return []string{systemDisk()}
	}
	return paths
}

// listSetting reads a comma-separated setting, without blank entries.
func listSetting(name string) []string {
	var values []string
	for _, value := range strings.Split(os.Getenv(name), ",") {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return values
}

// systemDisk returns the root of the disk the operating system is installed
// on: / on Linux and macOS, and the system drive, usually C:\, on Windows.
func systemDisk() string {
	if runtime.GOOS == "windows" {
		return os.Getenv("SystemDrive") + `\`
	}
	return "/"
}

// historyMaxEntries returns how many disks, sensors, network cards and GPUs
// each the history keeps per device, from HISTORY_MAX_ENTRIES, or
// history.DefaultMaxEntries when it is not set.
func historyMaxEntries() (int, error) {
	value := strings.TrimSpace(os.Getenv("HISTORY_MAX_ENTRIES"))
	if value == "" {
		return history.DefaultMaxEntries, nil
	}
	entries, err := strconv.Atoi(value)
	if err != nil || entries < 1 || entries > 10000 {
		return 0, fmt.Errorf("HISTORY_MAX_ENTRIES is %q; set it to a whole number from 1 to 10000", value)
	}
	return entries, nil
}

// pagePort returns the port the page is reachable on from the network, which
// a hub tells the devices it collects from: PUBLIC_PORT, or the port of
// LISTEN_ADDR. They differ in Docker, where the host's port is mapped to the
// container's 9393.
func pagePort(listenAddr string) (string, error) {
	value := strings.TrimSpace(os.Getenv("PUBLIC_PORT"))
	if value == "" {
		_, port, err := net.SplitHostPort(listenAddr)
		if err != nil {
			return "", fmt.Errorf("LISTEN_ADDR is %q; set it to an address and port such as :9393", listenAddr)
		}
		return port, nil
	}
	if port, err := strconv.Atoi(value); err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("PUBLIC_PORT is %q; set it to a port from 1 to 65535", value)
	}
	return value, nil
}

// bufferHours returns how long a data-only device keeps its minutes for a
// hub, from BUFFER_HOURS, or history.DefaultBufferSpan when it is not set.
func bufferHours() (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv("BUFFER_HOURS"))
	if value == "" {
		return history.DefaultBufferSpan, nil
	}
	hours, err := strconv.Atoi(value)
	if err != nil || hours < 1 || hours > 168 {
		return 0, fmt.Errorf("BUFFER_HOURS is %q; set it to a whole number of hours from 1 to 168", value)
	}
	return time.Duration(hours) * time.Hour, nil
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
