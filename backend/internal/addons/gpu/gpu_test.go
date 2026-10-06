package gpu

import (
	"reflect"
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
