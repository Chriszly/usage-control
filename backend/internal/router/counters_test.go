package router

import (
	"testing"
	"time"
)

func TestRate64TakesADecreaseAsAReset(t *testing.T) {
	var c counter
	now := time.Now()
	c.rate64(3_000_000_000, now, false)
	if rate, ok := c.rate64(1_000_000, now.Add(5*time.Second), false); ok {
		t.Errorf("a reset counted %v bytes per second", rate)
	}
	if rate, ok := c.rate64(1_500_000, now.Add(10*time.Second), false); !ok || rate != 100_000 {
		t.Errorf("got %v, %v", rate, ok)
	}
	// A 32-bit counter wraps around.
	var w counter
	w.rate(1<<32-1000, now, false)
	if rate, ok := w.rate(1000, now.Add(time.Second), false); !ok || rate != 2000 {
		t.Errorf("got %v, %v", rate, ok)
	}
}
