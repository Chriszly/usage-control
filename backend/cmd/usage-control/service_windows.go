//go:build windows

package main

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
)

// serviceName is the name the Windows installer registers the service under.
const serviceName = "UsageControl"

// serveRetryDelay is how long the service waits before it serves again after
// serving stopped on its own, such as while the port is still in use.
const serveRetryDelay = 10 * time.Second

// runAsService runs serve as a Windows service when the service manager
// started the program, and reports false when it was started any other way.
// Errors are also written to the Windows event log, because a service has no
// console to print them to.
func runAsService(serve func(context.Context) error) (bool, error) {
	isService, err := svc.IsWindowsService()
	if err != nil || !isService {
		return false, err
	}
	service := &windowsService{serve: serve, log: logError, retryDelay: serveRetryDelay}
	if err := svc.Run(serviceName, service); err != nil {
		service.err = fmt.Errorf("run as the Windows service %s: %w", serviceName, err)
	}
	if service.err != nil {
		logError(service.err)
	}
	return true, service.err
}

// logError writes err to the Windows event log, when the installer has
// registered the service as a source.
func logError(err error) {
	if log, openErr := eventlog.Open(serviceName); openErr == nil {
		_ = log.Error(1, err.Error())
		_ = log.Close()
	}
}

// windowsService answers the service manager while serve runs.
type windowsService struct {
	serve func(context.Context) error
	// log reports a failure of serve, and retryDelay is how long to wait
	// before serving again after one.
	log        func(error)
	retryDelay time.Duration
	err        error
}

// Execute runs serve until the service manager asks the service to stop. When
// serve fails, such as right after a reboot while another program still holds
// the port, the service stays running and serves again after retryDelay, as
// often as needed, so nobody has to start it by hand; each failure is written
// to the event log.
func (s *windowsService) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan error, 1)
	start := func() { go func() { stopped <- s.serve(ctx) }() }
	start()

	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	// retry is set while serve is not running, between a failure and the next try.
	var retry <-chan time.Time
	for {
		select {
		case err := <-stopped:
			if err == nil {
				return false, 0
			}
			s.log(fmt.Errorf("%w; trying again in %s", err, s.retryDelay))
			retry = time.After(s.retryDelay)
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
