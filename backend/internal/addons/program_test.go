package addons

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestProgramGivesUpOnACallThatDoesNotEnd(t *testing.T) {
	var p Program
	release := make(chan struct{})
	var calls atomic.Int32
	stuck := func() (string, error) {
		calls.Add(1)
		<-release
		return "late", nil
	}

	if _, err := p.Output(10*time.Millisecond, stuck); !errors.Is(err, ErrProgramStuck) {
		t.Fatalf("Output() of a call that does not end = %v, want ErrProgramStuck", err)
	}
	// While it is stuck, no other call is started.
	if _, err := p.Output(10*time.Millisecond, stuck); !errors.Is(err, ErrProgramStuck) || calls.Load() > 1 {
		t.Fatalf("Output() while stuck = %v after %d calls, want ErrProgramStuck after 1", err, calls.Load())
	}
	close(release)
	// Wait for the stuck call to end.
	deadline := time.Now().Add(5 * time.Second)
	for {
		out, err := p.Output(time.Second, func() (string, error) { return "answer", nil })
		if err == nil {
			if out != "answer" {
				t.Errorf("Output() = %q, want answer", out)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Output() after the call ended = %v, want the answer", err)
		}
		time.Sleep(time.Millisecond)
	}
}
