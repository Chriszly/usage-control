package gpu

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/Chriszly/usage-control/backend/internal/metrics"
)

func TestParseLeavesOutWhatTheGPUDoesNotReport(t *testing.T) {
	out := "0, NVIDIA GeForce RTX 3090, 30, 1695, 9751, 12, 0, P2, 350.00\n" +
		"1, NVIDIA RTX A2000 Laptop GPU, [N/A], 210, 405, [Not Supported], [N/A], P8, [N/A]\n" +
		"garbage\n"

	got := Parse(out)

	want := []GPU{
		{Index: "0", Name: "NVIDIA GeForce RTX 3090", Values: map[string]string{
			"fan": "30", "graphics-clock": "1695", "memory-clock": "9751", "encoder": "12",
			"decoder": "0", "performance-state": "P2", "power-limit": "350.00",
		}},
		{Index: "1", Name: "NVIDIA RTX A2000 Laptop GPU", Values: map[string]string{
			"graphics-clock": "210", "memory-clock": "405", "performance-state": "P8",
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Parse() = %+v, want %+v", got, want)
	}
}

func TestParseReadsTheGPUsBesideOneInAnErrorState(t *testing.T) {
	// What nvidia-smi prints, while exiting with an error, when one of its
	// GPUs fails.
	out := "0, NVIDIA GeForce RTX 3090, 30, 1695, 9751, 0, 0, P2, 350.00\n" +
		"Unable to determine the device handle for GPU0000:02:00.0: Unknown Error\n" +
		"[Unknown Error], [Unknown Error], [Unknown Error], [Unknown Error], [Unknown Error], " +
		"[Unknown Error], [Unknown Error], [Unknown Error], [Unknown Error]\n" +
		"2, NVIDIA GeForce RTX 3090, 31, 1700, 9751, 0, 0, P2, 350.00\n"

	got := Parse(out)

	if len(got) != 2 || got[0].Index != "0" || got[1].Index != "2" || got[1].Values["fan"] != "31" {
		t.Errorf("Parse() = %+v, want GPUs 0 and 2 without the failed one", got)
	}
}

func TestParseKeepsACommaInTheName(t *testing.T) {
	got := Parse("0, Odd, Name, 40, 1500, 7000, 0, 0, P0, 200\n")
	if len(got) != 1 || got[0].Name != "Odd, Name" || got[0].Values["fan"] != "40" {
		t.Errorf("Parse() = %+v, want one GPU named \"Odd, Name\" with its fan at 40", got)
	}
}

func TestExtrasOfOneGPU(t *testing.T) {
	gpus := Parse("0, NVIDIA GeForce RTX 4070, 35, 2475, 10501, 0, 3, P0, 200.00\n")

	got := Extras(gpus)

	if len(got) != 1 || got[0].ID != "gpu" || got[0].Title != "Graphics card" || got[0].Titles["de"] != "Grafikkarte" {
		t.Fatalf("Extras() = %+v, want the group gpu titled Graphics card", got)
	}
	items := got[0].Items
	if len(items) != 7 {
		t.Fatalf("items = %+v, want 7", items)
	}
	fan := items[0]
	if fan.ID != "0-fan" || fan.Label != "Fan" || fan.Labels["fr"] != "Ventilateur" ||
		fan.Unit != metrics.UnitPercent || *fan.Value != 35 || !fan.History {
		t.Errorf("fan = %+v, want 35 %% with history", fan)
	}
	clock := items[1]
	if clock.ID != "0-graphics-clock" || clock.Label != "Graphics clock (MHz)" || clock.Unit != metrics.UnitNumber || *clock.Value != 2475 {
		t.Errorf("graphics clock = %+v, want 2475 MHz", clock)
	}
	state := items[5]
	if state.ID != "0-performance-state" || state.Unit != metrics.UnitText || state.Text != "P0" || state.Value != nil || state.History {
		t.Errorf("performance state = %+v, want the text P0 without history", state)
	}
	limit := items[6]
	if limit.ID != "0-power-limit" || limit.Unit != metrics.UnitWatts || *limit.Value != 200 || limit.History {
		t.Errorf("power limit = %+v, want 200 W without history", limit)
	}
}

func TestExtrasNameEachOfSeveralGPUs(t *testing.T) {
	gpus := Parse("0, NVIDIA GeForce RTX 3090, 30, 1695, 9751, 0, 0, P2, 350\n" +
		"1, NVIDIA GeForce RTX 3090, 31, 1700, 9751, 0, 0, P2, 350\n")

	items := Extras(gpus)[0].Items

	if len(items) != 14 {
		t.Fatalf("items = %d, want 14", len(items))
	}
	second := items[7]
	if second.ID != "1-fan" || second.Label != "NVIDIA GeForce RTX 3090 (1): Fan" ||
		second.Labels["de"] != "NVIDIA GeForce RTX 3090 (1): Lüfter" {
		t.Errorf("second GPU's fan = %+v, want it named after the GPU", second)
	}
}

func TestExtrasOfNothing(t *testing.T) {
	if got := Extras(Parse("")); got != nil {
		t.Errorf("Extras() = %+v, want nothing", got)
	}
	if got := Extras(Parse("0, Old GPU, [N/A], [N/A], [N/A], [N/A], [N/A], [N/A], [N/A]\n")); got != nil {
		t.Errorf("Extras() of a GPU that reports nothing = %+v, want nothing", got)
	}
}

func TestReadWithoutNvidiaSMIReportsNothing(t *testing.T) {
	if got := (&Reader{}).Read(t.Context(), time.Time{}); got != nil {
		t.Errorf("Read() = %+v, want nothing", got)
	}
}

func TestReadKeepsWhatAFailingNvidiaSMIPrinted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in for nvidia-smi is a shell script")
	}
	// A stand-in for nvidia-smi that prints one GPU and fails for another.
	script := filepath.Join(t.TempDir(), "nvidia-smi")
	text := "#!/bin/sh\n" +
		"echo '0, NVIDIA GeForce RTX 4070, 35, 2475, 10501, 0, 3, P0, 200.00'\n" +
		"echo 'Unable to determine the device handle for GPU0000:02:00.0: Unknown Error'\n" +
		"exit 15\n"
	if err := os.WriteFile(script, []byte(text), 0o700); err != nil { //nolint:gosec // the test runs it
		t.Fatal(err)
	}
	r := &Reader{program: script}

	got := r.Read(t.Context(), time.Time{})

	if len(got) != 1 || len(got[0].Items) != 7 || got[0].Items[0].ID != "0-fan" || !r.failing {
		t.Errorf("Read() = %+v, want GPU 0's values from before nvidia-smi failed", got)
	}
}
