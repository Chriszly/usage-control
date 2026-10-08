//go:build windows

// Package winservice runs a program as a Windows service when the service
// manager starts it.
package winservice

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
)

// serveRetryDelay is how long the service waits before it serves again after
// serving stopped on its own, such as while the port is still in use. Each
// further failure in a row doubles the wait, up to maxServeRetryDelay, so a
// failure that lasts, such as a deleted add-on folder, does not try and write
// to the event log every few seconds for good.
const (
	serveRetryDelay    = 10 * time.Second
	maxServeRetryDelay = 5 * time.Minute
)

// Run runs serve as the Windows service name, the name the installer
// registers it under, when the service manager started the program, and
// reports false when it was started any other way. Errors are also written
// to the Windows event log, because a service has no console to print them to.
func Run(name string, serve func(context.Context) error) (bool, error) {
	isService, err := svc.IsWindowsService()
	if err != nil || !isService {
		return false, err
	}
	logError := func(err error) { logTo(name, err) }
	service := &windowsService{
		serve: serve, log: logError,
		retryDelay: serveRetryDelay, maxRetryDelay: maxServeRetryDelay,
	}
	if err := svc.Run(name, service); err != nil {
		service.err = fmt.Errorf("run as the Windows service %s: %w", name, err)
	}
	if service.err != nil {
		logError(service.err)
	}
	return true, service.err
}

// LogWarnings sends slog's warnings and errors to the Windows event log too,
// under source, when the service manager started the program: a service's
// standard error goes nowhere.
func LogWarnings(source string) {
	if isService, err := svc.IsWindowsService(); err != nil || !isService {
		return
	}
	log, err := eventlog.Open(source)
	if err != nil {
		return
	}
	// Open for as long as the program runs.
	slog.SetDefault(slog.New(newEventLogHandler(slog.NewTextHandler(os.Stderr, nil), log)))
}

// logTo writes err to the Windows event log under source, when the
// installer has registered the service as a source.
func logTo(source string, err error) {
	if log, openErr := eventlog.Open(source); openErr == nil {
		_ = log.Error(1, err.Error())
		_ = log.Close()
	}
}

// windowsService answers the service manager while serve runs.
type windowsService struct {
	serve func(context.Context) error
	// log reports a failure of serve, and retryDelay is how long to wait
	// before serving again after one, doubling with each failure in a row up
	// to maxRetryDelay.
	log           func(error)
	retryDelay    time.Duration
	maxRetryDelay time.Duration
	err           error
}

// Execute runs serve until the service manager asks the service to stop. When
// serve fails, such as right after a reboot while another program still holds
// the port, the service stays running and serves again after retryDelay, as
// often as needed, so nobody has to start it by hand. Each failure in a row
// doubles the wait, up to maxRetryDelay, and is written to the event log
// only when it differs from the one before or serve ran for at least
// retryDelay before it failed; a serve that ran for maxRetryDelay or longer
// before it failed starts over.
func (s *windowsService) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan error, 1)
	var started time.Time
	start := func() {
		started = time.Now()
		go func() { stopped <- s.serve(ctx) }()
	}
	start()
	maxDelay := max(s.maxRetryDelay, s.retryDelay)
	// delay is the wait before the next try, and logged the failure last
	// written to the event log, both kept while failures come in a row.
	delay, logged := time.Duration(0), ""

	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	// retry is set while serve is not running, between a failure and the next try.
	var retry <-chan time.Time
	for {
		select {
		case err := <-stopped:
			if err == nil {
				return false, 0
			}
			ran := time.Since(started)
			if delay == 0 || ran >= maxDelay {
				delay, logged = s.retryDelay, ""
			} else {
				if ran >= s.retryDelay {
					// Served for at least the first wait, so the failure
					// came back rather than went on.
					logged = ""
				}
				delay = min(2*delay, maxDelay)
			}
			if err.Error() != logged {
				logged = err.Error()
				s.log(fmt.Errorf("%w; trying again in %s, then less often, up to every %s, until it works", err, delay, maxDelay))
			}
			retry = time.After(delay)
		case <-retry:
			retry = nil
			start()
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				status <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				if retry == nil {
					s.err = <-stopped
				}
				return false, 0
			default:
			}
		}
	}
}
