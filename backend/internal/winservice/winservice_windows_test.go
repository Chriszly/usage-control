//go:build windows

package winservice

import (
	"context"
	"errors"
	"fmt"
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

// Each failure comes right away, far sooner than retryDelay, so none counts
// as one that came back after serving, which would be logged again.
func TestWindowsServiceLogsAFailureOnceAndBacksOff(t *testing.T) {
	var attempts atomic.Int32
	var logged []string
	served := make(chan struct{})
	service := &windowsService{
		log:           func(err error) { logged = append(logged, err.Error()) },
		retryDelay:    50 * time.Millisecond,
		maxRetryDelay: 10 * time.Second,
		serve: func(ctx context.Context) error {
			switch attempts.Add(1) {
			case 1, 2, 3, 4:
				return errors.New("no add-on folder")
			case 5:
				return errors.New("port in use")
			}
			close(served)
			<-ctx.Done()
			return nil
		},
	}
	requests := make(chan svc.ChangeRequest, 1)
	go func() {
		<-served
		requests <- svc.ChangeRequest{Cmd: svc.Stop}
	}()

	service.Execute(nil, requests, make(chan svc.Status, 2))
	want := []string{
		"no add-on folder; trying again in 50ms, then less often, up to every 10s, until it works",
		"port in use; trying again in 800ms, then less often, up to every 10s, until it works",
	}
	if len(logged) != len(want) || logged[0] != want[0] || logged[1] != want[1] {
		t.Errorf("logged %q, want %q", logged, want)
	}
}

// runUntilServed runs service with serve, which reports through served when it
// serves, and stops it then. It returns what service logged.
func runUntilServed(service *windowsService, serve func(ctx context.Context, served func()) error) []string {
	var logged []string
	service.log = func(err error) { logged = append(logged, err.Error()) }
	served := make(chan struct{})
	service.serve = func(ctx context.Context) error {
		return serve(ctx, func() { close(served) })
	}
	requests := make(chan svc.ChangeRequest, 1)
	go func() {
		<-served
		requests <- svc.ChangeRequest{Cmd: svc.Stop}
	}()
	service.Execute(nil, requests, make(chan svc.Status, 2))
	return logged
}

func TestWindowsServiceWaitsAtMostMaxRetryDelay(t *testing.T) {
	var attempts atomic.Int32
	service := &windowsService{retryDelay: time.Millisecond, maxRetryDelay: 100 * time.Millisecond}
	logged := runUntilServed(service, func(ctx context.Context, served func()) error {
		// A failure of its own each time, so each is logged with its wait.
		if n := attempts.Add(1); n <= 10 {
			return fmt.Errorf("failure %d", n)
		}
		served()
		<-ctx.Done()
		return nil
	})
	// 1ms, 2ms, 4ms and so on, until 100ms.
	waits := []string{"1ms", "2ms", "4ms", "8ms", "16ms", "32ms", "64ms", "100ms", "100ms", "100ms"}
	if len(logged) != len(waits) {
		t.Fatalf("logged %q, want %d failures", logged, len(waits))
	}
	for i, wait := range waits {
		want := fmt.Sprintf("failure %d; trying again in %s, then less often, up to every 100ms, until it works", i+1, wait)
		if logged[i] != want {
			t.Errorf("failure %d logged %q, want %q", i+1, logged[i], want)
		}
	}
}

func TestWindowsServiceStartsOverAfterServingForMaxRetryDelay(t *testing.T) {
	var attempts atomic.Int32
	service := &windowsService{retryDelay: 50 * time.Millisecond, maxRetryDelay: 100 * time.Millisecond}
	logged := runUntilServed(service, func(ctx context.Context, served func()) error {
		switch attempts.Add(1) {
		case 1:
			return errors.New("port in use")
		case 2:
			// Served for maxRetryDelay, then failed the same way: the wait
			// starts over at 50ms instead of doubling to 100ms.
			time.Sleep(100 * time.Millisecond)
			return errors.New("port in use")
		}
		served()
		<-ctx.Done()
		return nil
	})
	want := "port in use; trying again in 50ms, then less often, up to every 100ms, until it works"
	if len(logged) != 2 || logged[0] != want || logged[1] != want {
		t.Errorf("logged %q, want %q twice", logged, want)
	}
}

// A failure that comes back after serving for longer than retryDelay is
// logged again, even when the run was shorter than the wait before it.
func TestWindowsServiceLogsAFailureAgainThatComesBackAfterServing(t *testing.T) {
	var attempts atomic.Int32
	service := &windowsService{retryDelay: 50 * time.Millisecond, maxRetryDelay: 10 * time.Second}
	logged := runUntilServed(service, func(ctx context.Context, served func()) error {
		switch attempts.Add(1) {
		case 1, 2, 3:
			// Right away, sooner than retryDelay: logged only the first time.
			return errors.New("port in use")
		case 4:
			// Served for 100ms, longer than the 50ms retryDelay but shorter
			// than the 200ms wait before it, then failed the same way.
			time.Sleep(100 * time.Millisecond)
			return errors.New("port in use")
		case 5:
			// Right away again: not logged.
			return errors.New("port in use")
		}
		served()
		<-ctx.Done()
		return nil
	})
	want := []string{
		"port in use; trying again in 50ms, then less often, up to every 10s, until it works",
		"port in use; trying again in 400ms, then less often, up to every 10s, until it works",
	}
	if len(logged) != len(want) || logged[0] != want[0] || logged[1] != want[1] {
		t.Errorf("logged %q, want %q", logged, want)
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
		if want := "port in use; trying again in 1h0m0s, then less often, up to every 1h0m0s, until it works"; failure.Error() != want {
			t.Errorf("logged %q, want %q", failure, want)
		}
		requests <- svc.ChangeRequest{Cmd: svc.Stop}
	}()

	specific, code := service.Execute(nil, requests, make(chan svc.Status, 2))
	if specific || code != 0 || service.err != nil {
		t.Errorf("Execute() = %v, %d with error %v, want false, 0 without error", specific, code, service.err)
	}
}
