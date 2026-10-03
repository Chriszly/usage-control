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

	if w, ok := readWatts(energy); !ok || w != 12.5 {
		t.Errorf("readWatts(power_now) = %v, %v, want 12.5, true", w, ok)
	}
	if w, ok := readWatts(charge); !ok || w != 12 {
		t.Errorf("readWatts(current_now * voltage_now) = %v, %v, want 12, true", w, ok)
	}
	if h, ok := readHealth(energy); !ok || h != 90 {
		t.Errorf("readHealth(energy) = %v, %v, want 90, true", h, ok)
	}
	if h, ok := readHealth(charge); !ok || h != 80 {
		t.Errorf("readHealth(charge) = %v, %v, want 80, true", h, ok)
	}
	if _, ok := readHealth(t.TempDir()); ok {
		t.Error("readHealth() without files = true, want false")
	}
}
