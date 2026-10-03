package main

import (
	"encoding/binary"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const mascotDir = "../../../frontend/src/app/mascot"

// builtSite writes styles like the website build's, with the given fur color.
func builtSite(t *testing.T, fur string) string {
	t.Helper()
	dir := t.TempDir()
	css := `html{--mascot-fur: ` + fur + `;--mascot-ear: #ffb787;--mascot-muzzle: #ffdcc7;` +
		`--mascot-gauge: #964900;--mascot-eye: #ffffff;--mascot-dark: #311300}`
	if err := os.WriteFile(filepath.Join(dir, "styles-ABC123.css"), []byte(css), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDrawsTheMascotInTheThemesColors(t *testing.T) {
	for _, fur := range []string{"#e37100", "#3f51b5"} {
		colors, err := themeColors(builtSite(t, fur))
		if err != nil {
			t.Fatal(err)
		}
		bear, err := loadMascot(mascotDir, colors)
		if err != nil {
			t.Fatal(err)
		}
		want, _ := paint(fur, nil)

		img := bear.render(64, nil)
		// The forehead, between the ears and above the eyes.
		if got := img.NRGBAAt(32, 18); got != *want {
			t.Errorf("fur %s: forehead = %v, want %v", fur, got, *want)
		}
		if got := img.NRGBAAt(0, 0); got.A != 0 {
			t.Errorf("fur %s: corner = %v, want transparent", fur, got)
		}
	}
}

func TestDrawsTheStatusDot(t *testing.T) {
	colors, err := themeColors(builtSite(t, "#e37100"))
	if err != nil {
		t.Fatal(err)
	}
	bear, err := loadMascot(mascotDir, colors)
	if err != nil {
		t.Fatal(err)
	}
	green := color.NRGBA{0x2e, 0x9d, 0x4a, 0xff}
	img := bear.render(64, &green)
	if got := img.NRGBAAt(50, 50); got != green {
		t.Errorf("dot = %v, want %v", got, green)
	}
}

func TestWritesTheIcons(t *testing.T) {
	out := t.TempDir()
	if err := run(mascotDir, builtSite(t, "#e37100"), out); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"usage-control.ico", "running.ico", "stopped.ico", "paused.ico"} {
		data, err := os.ReadFile(filepath.Join(out, name)) //nolint:gosec // the test's own folder
		if err != nil {
			t.Fatal(err)
		}
		if kind, count := binary.LittleEndian.Uint16(data[2:]), int(binary.LittleEndian.Uint16(data[4:])); kind != 1 || count != len(sizes) {
			t.Fatalf("%s: type %d with %d images, want an icon with %d", name, kind, count, len(sizes))
		}
		for i, size := range sizes {
			entry := data[6+16*i:]
			length := binary.LittleEndian.Uint32(entry[8:])
			offset := binary.LittleEndian.Uint32(entry[12:])
			if int(entry[0]) != size || int(offset)+int(length) > len(data) || int(length) != 40+size*size*4+(size+31)/32*4*size {
				t.Errorf("%s: image %d is %d pixels, %d bytes at %d; want %d pixels inside the file", name, i, entry[0], length, offset, size)
			}
		}
	}
}

func TestAsksForTheWebsiteBuild(t *testing.T) {
	err := run(mascotDir, t.TempDir(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "npm run build") {
		t.Errorf("error = %v, want one that says to build the website", err)
	}
}

func TestRefusesWhatItCannotDraw(t *testing.T) {
	colors := map[string]string{"--mascot-fur": "#e37100"}
	styles := classStyles(`.fur { fill: var(--mascot-fur); } .other { fill: var(--mascot-other); }
		.line { fill: none; stroke: #000000; stroke-width: 1; stroke-linecap: square; }`)
	for _, element := range []struct {
		name  string
		attrs map[string]string
	}{
		{"circle", map[string]string{"class": "missing", "cx": "1", "cy": "1", "r": "1"}},
		{"circle", map[string]string{"class": "other", "cx": "1", "cy": "1", "r": "1"}},
		{"circle", map[string]string{"class": "fur", "cx": "1", "cy": "x", "r": "1"}},
		{"path", map[string]string{"class": "fur", "d": "M1 1 L2 2"}},
		{"path", map[string]string{"class": "line", "d": "M1 1 L2 2"}},
	} {
		if _, err := newShape(element.name, element.attrs, styles, colors); err == nil {
			t.Errorf("<%s %v>: error = nil, want one", element.name, element.attrs)
		}
	}
}

func TestFlatten(t *testing.T) {
	got, err := flatten("M16 25 V26.6 H18 l1 1")
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]float64{{16, 25}, {16, 26.6}, {18, 26.6}, {19, 27.6}}
	if len(got) != len(want) {
		t.Fatalf("points = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("point %d = %v, want %v", i, got[i], want[i])
		}
	}

	// The gauge: half a circle over its center, ending at the end point.
	arcPoints, err := flatten("M7.2 17 A3.4 3.4 0 0 1 13.8 17")
	if err != nil {
		t.Fatal(err)
	}
	end := arcPoints[len(arcPoints)-1]
	middle := arcPoints[len(arcPoints)/2]
	if math.Abs(end[0]-13.8) > 1e-9 || math.Abs(end[1]-17) > 1e-9 || middle[1] > 15 {
		t.Errorf("arc ends at %v through %v, want it to end at 13.8,17 over the top", end, middle)
	}

	for _, d := range []string{"", "L1 1", "M1", "M1 1", "M1 1 C1 1 2 2 3 3", "M1 1 M2 2"} {
		if _, err := flatten(d); err == nil {
			t.Errorf("flatten(%q) error = nil, want one", d)
		}
	}
}
