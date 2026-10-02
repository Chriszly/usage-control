//go:build windows

package main

import (
	"context"
	"fmt"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
)

// serviceName is the name the Windows installer registers the service under.
const serviceName = "UsageControl"

// runAsService runs serve as a Windows service when the service manager
// started the program, and reports false when it was started any other way.
// Errors are also written to the Windows event log, because a service has no
// console to print them to.
func runAsService(serve func(context.Context) error) (bool, error) {
	isService, err := svc.IsWindowsService()
	if err != nil || !isService {
		return false, err
	}
	service := &windowsService{serve: serve}
	if err := svc.Run(serviceName, service); err != nil {
		service.err = fmt.Errorf("run as the Windows service %s: %w", serviceName, err)
	}
	if service.err != nil {
		if log, err := eventlog.Open(serviceName); err == nil {
			_ = log.Error(1, service.err.Error())
			_ = log.Close()
		}
	}
	return true, service.err
}

// windowsService answers the service manager while serve runs.
type windowsService struct {
	serve func(context.Context) error
	err   error
}

// Execute runs serve until the service manager asks the service to stop, or
// serve stops on its own, such as when the port is already in use.
func (s *windowsService) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- s.serve(ctx) }()

	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case s.err = <-stopped:
			// A non-zero exit code makes the service manager restart the
			// service, as the installer sets it up to.
			if s.err != nil {
				return true, 1
			}
			return false, 0
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				status <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				s.err = <-stopped
				return false, 0
			default:
			}
		}
	}
}
