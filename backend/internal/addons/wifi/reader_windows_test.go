package wifi

import "testing"

// The machine may have no Wi-Fi, as a CI runner has none; the reader must
// then report nothing rather than fail, and every reading it does report
// must make sense.
func TestNewReaderReadsThisMachine(t *testing.T) {
	read := NewReader()
	for range 2 {
		for _, r := range read() {
			if r.Interface == "" || len(r.Key) != 32 || r.QualityPercent < 0 || r.QualityPercent > 100 || (r.HasSignal && r.SignalDBm >= 0) {
				t.Errorf("reading %+v makes no sense", r)
			}
		}
	}
	readings, problem := readWLAN()
	t.Logf("read %+v; problem: %q", readings, problem)
}
