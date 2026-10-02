package metrics

import (
	"reflect"
	"testing"
)

func TestSortGPUsNumbersGPUsWithTheSameName(t *testing.T) {
	got := sortGPUs([]GPU{{Name: "Radeon"}, {Name: "GeForce"}, {Name: "Radeon"}})

	want := []GPU{{Name: "GeForce"}, {Name: "Radeon 1"}, {Name: "Radeon 2"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sortGPUs() = %+v, want %+v", got, want)
	}
}

func TestMemoryUsedPercent(t *testing.T) {
	if p, ok := (GPU{MemoryTotalBytes: 400, MemoryUsedBytes: 100}).MemoryUsedPercent(); !ok || p != 25 {
		t.Errorf("MemoryUsedPercent() = %v, %v, want 25, true", p, ok)
	}
	if _, ok := (GPU{}).MemoryUsedPercent(); ok {
		t.Error("MemoryUsedPercent() of a GPU without own memory reports a value")
	}
}

func TestGPUsFromCounters(t *testing.T) {
	const (
		card       = "luid_0x00000000_0x0000D1A5_phys_0"
		integrated = "luid_0x00000000_0x0000E2B6_phys_0"
		software   = "luid_0x00000000_0x0000F3C7_phys_0"
	)
	engines := map[string]float64{
		// Two processes on the 3D engine and one on video decode.
		"pid_100_" + card + "_eng_0_engtype_3D":          30,
		"pid_200_" + card + "_eng_0_engtype_3D":          25,
		"pid_100_" + card + "_eng_3_engtype_VideoDecode": 40,
		"pid_100_" + integrated + "_eng_0_engtype_3D":    5,
		"pid_100_" + software + "_eng_0_engtype_3D":      1,
		"pid_300_luid_garbage_eng_0_engtype_3D":          99,
		"pid_400_" + integrated + "_eng_1_engtype_Copy":  150,
	}
	memory := map[string]float64{card: 1 << 30, integrated: 1 << 20}
	adapters := map[luid]adapter{
		0xD1A5: {name: "NVIDIA GeForce RTX 4070", memoryBytes: 12 << 30},
		0xF3C7: {name: softwareAdapter},
	}

	got := gpusFromCounters(engines, memory, adapters)

	want := []GPU{
		// Unknown to DirectX, without memory of its own; one engine is over 100 %.
		{Name: "GPU", UsagePercent: 100},
		{Name: "NVIDIA GeForce RTX 4070", UsagePercent: 55, MemoryTotalBytes: 12 << 30, MemoryUsedBytes: 1 << 30},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("gpusFromCounters() = %+v, want %+v", got, want)
	}
}
