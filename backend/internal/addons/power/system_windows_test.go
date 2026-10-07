package power

import "testing"

// TestWindowsSystemReadsThisMachine checks the energy meters and batteries
// can be read on a real Windows machine without crashing. The CI machine may
// have neither, so any list will do.
func TestWindowsSystemReadsThisMachine(t *testing.T) {
	s := newSystem()
	s.read()
	readings := s.read()
	for _, r := range readings {
		if r.Watts < 0 {
			t.Errorf("%s = %v W, want at least 0", r.ID, r.Watts)
		}
	}
	t.Logf("energy meters: %v, readings: %+v", s.meters != 0, readings)
}
