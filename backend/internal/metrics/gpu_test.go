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

func TestAllKnownTellsWhenACounterNamesAnUnknownGPU(t *testing.T) {
	engines := map[string]float64{
		"pid_1_luid_0x00000000_0x0000D1A5_phys_0_eng_0_engtype_3D":   10,
		"pid_1_luid_0x00000000_0x0000D1A5_phys_0_eng_1_engtype_Copy": 1,
		"something else": 2,
	}
	known := map[luid]adapter{0xD1A5: {name: "Radeon"}}
	if !allKnown(engines, known) {
		t.Error("allKnown() = false with every GPU known, want true")
	}
	engines["pid_2_luid_0x00000000_0x0000BEEF_phys_0_eng_0_engtype_3D"] = 5
	if allKnown(engines, known) {
		t.Error("allKnown() = true with an unknown GPU, want false")
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

func TestNvidiaGPUKey(t *testing.T) {
	uuid := "GPU-1A2B3C4D-5e6f-7a8b-9c0d-112233445566"
	tests := []struct {
		index, uuid string
		gpus        int
		want        string
	}{
		{"0", uuid, 1, "0"},
		{"1", uuid, 1, "1a2b3c4d"},
		{"0", uuid, 2, "1a2b3c4d"},
		{"1", "[N/A]", 2, "1"},
		{"1", "", 2, "1"},
	}
	for _, tt := range tests {
		if got := NvidiaGPUKey(tt.index, tt.uuid, tt.gpus); got != tt.want {
			t.Errorf("NvidiaGPUKey(%q, %q, %d) = %q, want %q", tt.index, tt.uuid, tt.gpus, got, tt.want)
		}
	}
}
