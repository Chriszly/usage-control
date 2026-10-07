//go:build linux || windows

package metrics

import (
	"context"
	"reflect"
	"sync/atomic"
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

func TestNvidiaSMIAnswersInTheBackground(t *testing.T) {
	release := make(chan struct{})
	calls := 0
	n := &nvidiaSMI{
		program: "nvidia-smi",
		query: func(ctx context.Context, _ string) []GPU {
			calls++
			if calls == 1 {
				return []GPU{{Name: "first"}}
			}
			<-release
			if _, ok := ctx.Deadline(); !ok {
				t.Error("nvidia-smi runs without a time limit")
			}
			return []GPU{{Name: "second"}}
		},
	}

	// A quick answer is waited for.
	if got := n.read(context.Background()); len(got) != 1 || got[0].Name != "first" {
		t.Fatalf("read() = %+v, want the first answer", got)
	}
	// Within the interval, nvidia-smi is not asked again.
	n.read(context.Background())
	if calls != 1 {
		t.Fatalf("nvidia-smi called %d times within the interval, want 1", calls)
	}

	// A slow answer is not: the reading takes the last one, here because
	// it ends at once, and nvidia-smi goes on.
	n.mu.Lock()
	n.at = time.Now().Add(-nvidiaSMIInterval)
	n.mu.Unlock()
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	if got := n.read(ended); len(got) != 1 || got[0].Name != "first" {
		t.Errorf("read() while nvidia-smi is slow = %+v, want the last answer", got)
	}
	n.mu.Lock()
	running := n.running
	n.mu.Unlock()
	close(release)
	<-running
	if got := n.read(context.Background()); len(got) != 1 || got[0].Name != "second" || calls != 2 {
		t.Errorf("read() after nvidia-smi answered = %+v after %d calls, want its answer", got, calls)
	}
}

func TestNvidiaSMIThatDoesNotEndReportsNoGPU(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	n := &nvidiaSMI{
		program: "nvidia-smi",
		query: func(context.Context, string) []GPU {
			calls.Add(1)
			// Stuck: it ignores its context.
			<-release
			return []GPU{{Name: "new"}}
		},
		gpus: []GPU{{Name: "old"}},
		at:   time.Now().Add(-nvidiaSMIInterval),
	}
	ended, cancel := context.WithCancel(context.Background())
	cancel()

	if got := n.read(ended); len(got) != 1 || got[0].Name != "old" {
		t.Fatalf("read() while nvidia-smi is slow = %+v, want the last answer", got)
	}
	n.mu.Lock()
	n.started = time.Now().Add(-nvidiaSMITimeout - nvidiaSMIStuck - time.Second)
	running := n.running
	n.mu.Unlock()
	if got := n.read(ended); got != nil {
		t.Errorf("read() while nvidia-smi is stuck = %+v, want no GPU", got)
	}

	close(release)
	<-running
	if calls.Load() != 1 {
		t.Errorf("nvidia-smi started %d times while stuck, want once", calls.Load())
	}
	if got := n.read(context.Background()); len(got) != 1 || got[0].Name != "new" {
		t.Errorf("read() once nvidia-smi ended = %+v, want its answer", got)
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

func TestNvidiaSMITemperaturesOfIdenticalGPUsAreNumbered(t *testing.T) {
	first, second := 48.0, 61.0
	n := &nvidiaSMI{
		program: "nvidia-smi",
		gpus:    []GPU{{Name: "NVIDIA GeForce RTX 3090", Celsius: &first}, {Name: "NVIDIA GeForce RTX 3090", Celsius: &second}},
		at:      time.Now(),
	}

	want := []Temperature{{Sensor: "NVIDIA GeForce RTX 3090 1", Celsius: 48}, {Sensor: "NVIDIA GeForce RTX 3090 2", Celsius: 61}}
	if got := n.temperatures(context.Background()); !reflect.DeepEqual(got, want) {
		t.Errorf("temperatures() = %+v, want %+v", got, want)
	}
	if n.gpus[0].Name != "NVIDIA GeForce RTX 3090" {
		t.Errorf("temperatures() renamed the cached GPU to %q", n.gpus[0].Name)
	}
}
