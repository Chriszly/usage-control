//go:build windows

package main

import (
	"context"
	"errors"
	"testing"

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

func TestWindowsServiceReportsFailure(t *testing.T) {
	failure := errors.New("port in use")
	service := &windowsService{serve: func(context.Context) error { return failure }}

	specific, code := service.Execute(nil, make(chan svc.ChangeRequest), make(chan svc.Status, 1))
	if !specific || code != 1 || !errors.Is(service.err, failure) {
		t.Errorf("Execute() = %v, %d with error %v, want true, 1 with %v", specific, code, service.err, failure)
	}
}
