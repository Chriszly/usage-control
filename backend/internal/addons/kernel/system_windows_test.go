package kernel

import (
	"testing"
	"time"
)

// TestSystemReaderReadsWindows reads the machine it runs on, a Windows one.
func TestSystemReaderReadsWindows(t *testing.T) {
	r := NewSystemReader()
	start := time.Now()
	r.Read(start)
	time.Sleep(time.Second)

	got := r.Read(start.Add(time.Second))

	t.Logf("Read() = %v", got)
	for _, id := range []string{contextSwitches, interrupts, handles, tcpEstablished, retransmissions} {
		if _, ok := got[id]; !ok {
			t.Errorf("Read() lacks %s", id)
		}
	}
	if got[handles] < 1 || got[contextSwitches] <= 0 {
		t.Errorf("Read() = %v, want handles and context switches", got)
	}
	for _, id := range []string{newProcesses, openFiles, sockets} {
		if _, ok := got[id]; ok {
			t.Errorf("Read() has %s, which Windows does not count", id)
		}
	}
}
