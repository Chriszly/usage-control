//go:build windows

package winservice

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
)

func TestWindowsServiceStopsWhenAsked(t *testing.T) {
	service := &windowsService{serve: func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	}}
	requests := make(chan svc.ChangeRequest, 1)
	status := make(chan svc.Status, 2)
	requests <- svc.ChangeRequest{Cmd: svc.Stop}

	specific, code := service.Execute(nil, requests, status)
	if specific || code != 0 || service.err != nil {
		t.Errorf("Execute() = %v, %d with error %v, want false, 0 without error", specific, code, service.err)
	}
	if got := (<-status).State; got != svc.Running {
		t.Errorf("first status = %v, want Running", got)
	}
	if got := (<-status).State; got != svc.StopPending {
		t.Errorf("second status = %v, want StopPending", got)
	}
}

func TestWindowsServiceServesAgainAfterAFailure(t *testing.T) {
	var attempts atomic.Int32
	served := make(chan struct{})
	service := &windowsService{
		log:        func(error) {},
		retryDelay: 10 * time.Millisecond,
		serve: func(ctx context.Context) error {
			if attempts.Add(1) == 1 {
				return errors.New("port in use")
			}
			close(served)
			<-ctx.Done()
			return nil
		},
	}
	requests := make(chan svc.ChangeRequest, 1)
	status := make(chan svc.Status, 2)
	go func() {
		<-served
		requests <- svc.ChangeRequest{Cmd: svc.Stop}
	}()

	specific, code := service.Execute(nil, requests, status)
	if specific || code != 0 || service.err != nil {
		t.Errorf("Execute() = %v, %d with error %v, want false, 0 without error", specific, code, service.err)
	}
	if got := attempts.Load(); got != 2 {
		t.Errorf("serve ran %d times, want 2", got)
	}
}

func TestWindowsServiceStopsWhileWaitingToServeAgain(t *testing.T) {
	failures := make(chan error, 1)
	service := &windowsService{
		log:        func(err error) { failures <- err },
		retryDelay: time.Hour,
		serve:      func(context.Context) error { return errors.New("port in use") },
	}
	requests := make(chan svc.ChangeRequest, 1)
	go func() {
		failure := <-failures
		if want := "port in use; trying again in 1h0m0s"; failure.Error() != want {
			t.Errorf("logged %q, want %q", failure, want)
		}
		requests <- svc.ChangeRequest{Cmd: svc.Stop}
	}()

	specific, code := service.Execute(nil, requests, make(chan svc.Status, 2))
	if specific || code != 0 || service.err != nil {
		t.Errorf("Execute() = %v, %d with error %v, want false, 0 without error", specific, code, service.err)
	}
}
