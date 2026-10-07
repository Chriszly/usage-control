package wifi

import (
	"context"
	"testing"
)

// The machine may have no Wi-Fi, as a CI runner has none; the reader must
// then report nothing rather than fail, and every reading it does report
// must make sense. Once its context is done, it reads nothing more.
func TestNewReaderReadsThisMachine(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	read := NewReader()
	for range 2 {
		for _, r := range read(ctx) {
			if r.Interface == "" || len(r.Key) != 32 || r.QualityPercent < 0 || r.QualityPercent > 100 || (r.HasSignal && r.SignalDBm >= 0) {
				t.Errorf("reading %+v makes no sense", r)
			}
		}
	}
	if handle, problem := openWLAN(); problem == "" {
		readings, connected, problem, ok := readWLAN(handle)
		t.Logf("read %+v of %d connected; problem: %q, listed: %v", readings, connected, problem, ok)
		_, _, _ = wlanCloseHandle.Call(uintptr(handle), 0)
	} else {
		t.Logf("problem: %q", problem)
	}

	cancel()
	// The handle is closed after ctx is done, in a goroutine of its own;
	// a read waits for it and then finds the reader closed.
	for range 100 {
		if read(context.Background()) == nil {
			return
		}
	}
	t.Error("the reader still reads after its context is done")
}
