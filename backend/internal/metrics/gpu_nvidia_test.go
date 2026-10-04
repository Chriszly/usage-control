//go:build linux || windows

package metrics

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestParseNvidiaSMI(t *testing.T) {
	out := "NVIDIA GeForce RTX 3080, 42, 1024, 10240, 61\n" +
		"Tesla T4, 7, [N/A], [N/A], [N/A]\n" +
		"Broken GPU, [Not Supported], 1, 2, 3\n"

	celsius := 61.0
	want := []GPU{
		{Name: "NVIDIA GeForce RTX 3080", UsagePercent: 42, MemoryUsedBytes: 1024 << 20, MemoryTotalBytes: 10240 << 20, Celsius: &celsius},
		{Name: "Tesla T4", UsagePercent: 7},
	}
	if got := parseNvidiaSMI(out); !reflect.DeepEqual(got, want) {
		t.Errorf("parseNvidiaSMI() = %+v, want %+v", got, want)
	}
}

func TestNvidiaSMITemperaturesAreNamedAfterTheGPU(t *testing.T) {
	celsius := 48.0
	// A fresh answer, so nvidia-smi is not started.
	n := &nvidiaSMI{
		program: "nvidia-smi",
		gpus:    []GPU{{Name: "NVIDIA GeForce RTX 5060 Ti", Celsius: &celsius}, {Name: "Tesla T4"}},
		at:      time.Now(),
	}

	want := []Temperature{{Sensor: "NVIDIA GeForce RTX 5060 Ti", Celsius: 48}}
	if got := n.temperatures(context.Background()); !reflect.DeepEqual(got, want) {
		t.Errorf("temperatures() = %+v, want %+v", got, want)
	}
	if got := (&nvidiaSMI{}).temperatures(context.Background()); got != nil {
		t.Errorf("temperatures() without nvidia-smi = %+v, want none", got)
	}
}
