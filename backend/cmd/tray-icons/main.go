// Command tray-icons draws the icons of the Windows tray program from the
// website's mascot, in the colors of the website's theme.
//
// It reads the bear from frontend/src/app/mascot (mascot.html and
// mascot.css) and the colors of the built website (the --mascot-* values that
// styles.scss derives from its Material $palette), so changing the palette
// and building the website again recolors the tray icon too. Each icon gets a
// status dot in a fixed color: green while the service answers, red when it
// is stopped or does not answer, grey while it is paused.
//
// Build the website first, then run it from backend/:
//
//	go run ./cmd/tray-icons
package main

import (
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
)

// sizes are the sizes in each icon file, in pixels: the tray at 100 to 200 %
// display scaling and the larger ones Windows picks for its settings pages.
var sizes = []int{16, 20, 24, 32, 40, 48, 64}

// dot is a status dot drawn over the bear's lower right.
type dot struct {
	file  string
	color color.NRGBA
}

var dots = []dot{
	{"running.ico", color.NRGBA{0x2e, 0x9d, 0x4a, 0xff}},
	{"stopped.ico", color.NRGBA{0xd9, 0x30, 0x25, 0xff}},
	{"paused.ico", color.NRGBA{0x8a, 0x8a, 0x8a, 0xff}},
}

func main() {
	mascot := flag.String("mascot", "../frontend/src/app/mascot", "folder with mascot.html and mascot.css")
	site := flag.String("site", "internal/web/files/build", "the built website, for the theme's colors")
	out := flag.String("out", "cmd/usage-control-tray/icons", "folder to write the icons to")
	flag.Parse()
	if err := run(*mascot, *site, *out); err != nil {
		fmt.Fprintln(os.Stderr, "tray-icons:", err)
		os.Exit(1)
	}
}

func run(mascotDir, siteDir, outDir string) error {
	colors, err := themeColors(siteDir)
	if err != nil {
		return err
	}
	bear, err := loadMascot(mascotDir, colors)
	if err != nil {
		return err
	}
	// The plain bear is the installer's icon, for the list of installed apps.
	if err := writeIcon(filepath.Join(outDir, "usage-control.ico"), bear, nil); err != nil {
		return err
	}
	for _, d := range dots {
		if err := writeIcon(filepath.Join(outDir, d.file), bear, &d.color); err != nil {
			return err
		}
	}
	return nil
}

func writeIcon(path string, bear *drawing, status *color.NRGBA) error {
	data, err := encodeICO(sizes, func(size int) *image.NRGBA { return bear.render(size, status) })
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("write %s: the folder is missing; run this from backend/ or set -out", path)
		}
		return err
	}
	return nil
}
