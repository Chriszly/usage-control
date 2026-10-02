package metrics

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const v3dHeader = "queue\ttimestamp\tjobs\truntime\n"

func TestV3DUsageIsTheBusiestQueue(t *testing.T) {
	before, err := parseV3DStats(v3dHeader + "bin\t1000000000\t5\t100000000\nrender\t1000000000\t5\t200000000\n")
	if err != nil {
		t.Fatal(err)
	}
	// In 2 seconds bin was busy for 0.2 s and render for 1.2 s.
	after, err := parseV3DStats(v3dHeader + "bin\t3000000000\t9\t300000000\nrender\t3000000000\t9\t1400000000\n")
	if err != nil {
		t.Fatal(err)
	}

	if got := v3dUsage(before, after); got != 60 {
		t.Errorf("v3dUsage() = %v, want 60", got)
	}
	if got := v3dUsage(v3dReading{}, after); got != 0 {
		t.Errorf("v3dUsage() of the first reading = %v, want 0", got)
	}
}

func TestParseV3DStatsRefusesOtherFormats(t *testing.T) {
	for _, text := range []string{"", v3dHeader, v3dHeader + "render\t1\t2\n", v3dHeader + "render\tx\t1\t2\n"} {
		if _, err := parseV3DStats(text); err == nil {
			t.Errorf("parseV3DStats(%q) error = nil, want an error", text)
		}
	}
}

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

func TestGPUReaderReadsAMDAndVideoCoreFromSys(t *testing.T) {
	sys := t.TempDir()
	write := func(path, text string) {
		t.Helper()
		path = filepath.Join(sys, "class", "drm", path)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("card0/device/gpu_busy_percent", "37\n")
	write("card0/device/mem_info_vram_total", "8589934592\n")
	write("card0/device/mem_info_vram_used", "2147483648\n")
	write("card0/device/hwmon/hwmon3/temp1_input", "52000\n")
	write("card1/device/gpu_stats", v3dHeader+"render\t1000\t1\t100\n")
	write("card1-HDMI-A-1/device/gpu_busy_percent", "99\n")
	write("card2/device/vendor", "0x8086\n") // a GPU without usage, such as Intel

	reader := &gpuReader{sysDir: sys, v3d: map[string]v3dReading{}}
	first := reader.read(context.Background())
	write("card1/device/gpu_stats", v3dHeader+"render\t2000\t2\t350\n")
	second := reader.read(context.Background())

	celsius := 52.0
	amd := GPU{Name: "AMD GPU", UsagePercent: 37, MemoryTotalBytes: 8 << 30, MemoryUsedBytes: 2 << 30, Celsius: &celsius}
	if want := []GPU{amd, {Name: "VideoCore GPU"}}; !reflect.DeepEqual(first, want) {
		t.Errorf("first read() = %+v, want %+v", first, want)
	}
	if want := []GPU{amd, {Name: "VideoCore GPU", UsagePercent: 25}}; !reflect.DeepEqual(second, want) {
		t.Errorf("second read() = %+v, want %+v", second, want)
	}
}
