// Command usage-control serves a website that shows the usage of the machine
// it runs on.
//
// Settings come from environment variables:
//
//	LISTEN_ADDR     address to listen on (default ":8080")
//	DISK_PATHS      comma-separated paths whose disk usage is shown (default "/")
//	DATABASE_PATH   SQLite file the history is kept in (default "usage-control.db")
//	RETENTION_DAYS  days of history to keep; older values are deleted (default 30)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/history"
	"github.com/Chriszly/usage-control/backend/internal/metrics"
	"github.com/Chriszly/usage-control/backend/internal/server"
	"github.com/Chriszly/usage-control/backend/internal/web"
)

func main() {
	if err := run(); err != nil {
		slog.Error("usage-control stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	retention, err := retentionDays()
	if err != nil {
		return err
	}

	collector, err := metrics.NewCollector(context.Background(), diskPaths())
	if err != nil {
		return fmt.Errorf("check DISK_PATHS: %w; mount each path read-only in compose.yaml", err)
	}
	// The recorder measures with its own collector, so CPU usage and network
	// speed in the history are averages over its own interval.
	recorderCollector, err := metrics.NewCollector(context.Background(), diskPaths())
	if err != nil {
		return err
	}

	databasePath := os.Getenv("DATABASE_PATH")
	if databasePath == "" {
		databasePath = "usage-control.db"
	}
	store, err := history.Open(context.Background(), databasePath)
	if err != nil {
		return fmt.Errorf("open the history database %s: %w; set DATABASE_PATH to a writable file", databasePath, err)
	}
	defer func() { _ = store.Close() }()

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           server.New(collector, server.History{Reader: store, Retention: retention}, web.Files()),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	recorder := &history.Recorder{Store: store, Collector: recorderCollector, Retention: retention}
	recorded := make(chan struct{})
	go func() {
		defer close(recorded)
		recorder.Run(ctx)
	}()
	defer func() {
		stop()
		<-recorded
	}()

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

// diskPaths returns the paths from DISK_PATHS, or the root filesystem when it
// is not set.
func diskPaths() []string {
	value := os.Getenv("DISK_PATHS")
	if strings.TrimSpace(value) == "" {
		return []string{"/"}
	}
	var paths []string
	for _, path := range strings.Split(value, ",") {
		if path = strings.TrimSpace(path); path != "" {
			paths = append(paths, path)
		}
	}
	return paths
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
