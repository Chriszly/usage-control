package metrics

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadWattsAndHealth(t *testing.T) {
	write := func(dir, name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A battery that reports energy and power.
	energy := t.TempDir()
	write(energy, "power_now", "12500000\n")
	write(energy, "energy_full", "45000000\n")
	write(energy, "energy_full_design", "50000000\n")
	// A battery that reports charge, current and voltage.
	charge := t.TempDir()
	write(charge, "current_now", "1000000\n")
	write(charge, "voltage_now", "12000000\n")
	write(charge, "charge_full", "4000000\n")
	write(charge, "charge_full_design", "5000000\n")

	if w := readWatts(energy); w == nil || *w != 12.5 {
		t.Errorf("readWatts(power_now) = %v, want 12.5", w)
	}
	if w := readWatts(charge); w == nil || *w != 12 {
		t.Errorf("readWatts(current_now * voltage_now) = %v, want 12", w)
	}
	if h := readHealth(energy); h == nil || *h != 90 {
		t.Errorf("readHealth(energy) = %v, want 90", h)
	}
	if h := readHealth(charge); h == nil || *h != 80 {
		t.Errorf("readHealth(charge) = %v, want 80", h)
	}
	if h := readHealth(t.TempDir()); h != nil {
		t.Errorf("readHealth() without files = %v, want nil", *h)
	}
}
