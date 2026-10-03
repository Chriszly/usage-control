package main

import (
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// drawing is the mascot as shapes, painted in order like the SVG does.
type drawing struct {
	// width and height are the SVG's viewBox size.
	width, height float64
	shapes        []shape
}

// shape is one SVG element with the colors of its CSS class.
type shape struct {
	element string
	// inside tells whether a point is in a filled circle or ellipse.
	inside func(x, y float64) bool
	// line is a path's points, for a stroke.
	line [][2]float64

	fill, stroke *color.NRGBA
	strokeWidth  float64
	roundCaps    bool
	// left, top, right and bottom hold everything the shape paints, so the
	// pixels outside are skipped.
	left, top, right, bottom float64
}

var (
	cssVariable  = regexp.MustCompile(`--mascot-([a-z]+):\s*(#[0-9a-fA-F]{6})\b`)
	svgElement   = regexp.MustCompile(`<(circle|ellipse|path)\b([^>]*?)/>`)
	svgAttribute = regexp.MustCompile(`([a-zA-Z-]+)="([^"]*)"`)
	cssRule      = regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`)
	cssComment   = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

// themeColors reads the --mascot-* colors from the built website's styles.
func themeColors(siteDir string) (map[string]string, error) {
	files, err := filepath.Glob(filepath.Join(siteDir, "styles-*.css"))
	if err != nil {
		return nil, err
	}
	colors := map[string]string{}
	for _, file := range files {
		data, err := os.ReadFile(file) //nolint:gosec // the website build, found in the folder given to this tool
		if err != nil {
			return nil, err
		}
		for _, match := range cssVariable.FindAllStringSubmatch(string(data), -1) {
			if name := "--mascot-" + match[1]; colors[name] == "" {
				colors[name] = match[2]
			}
		}
	}
	if len(colors) == 0 {
		return nil, fmt.Errorf("no --mascot-* colors in %s/styles-*.css; build the website first: npm ci && npm run build in frontend/", siteDir)
	}
	return colors, nil
}

// loadMascot reads the bear from mascot.html, colored by the classes in
// mascot.css with the theme's colors.
func loadMascot(dir string, colors map[string]string) (*drawing, error) {
	html, err := os.ReadFile(filepath.Join(dir, "mascot.html")) //nolint:gosec // the folder given to this tool
	if err != nil {
		return nil, err
	}
	css, err := os.ReadFile(filepath.Join(dir, "mascot.css")) //nolint:gosec // the folder given to this tool
	if err != nil {
		return nil, err
	}
	styles := classStyles(string(css))

	d := &drawing{}
	if _, err := fmt.Sscanf(svgAttributes(string(html))["viewBox"], "0 0 %g %g", &d.width, &d.height); err != nil || d.width <= 0 || d.height <= 0 {
		return nil, fmt.Errorf("mascot.html: the svg needs a viewBox \"0 0 <width> <height>\"")
	}
	for _, match := range svgElement.FindAllStringSubmatch(string(html), -1) {
		s, err := newShape(match[1], attributes(match[2]), styles, colors)
		if err != nil {
			return nil, fmt.Errorf("mascot.html: <%s%s/>: %w", match[1], match[2], err)
		}
		d.shapes = append(d.shapes, s)
	}
	if len(d.shapes) == 0 {
		return nil, fmt.Errorf("mascot.html: found no circle, ellipse or path")
	}
	return d, nil
}

// svgAttributes returns the attributes of the <svg> element.
func svgAttributes(html string) map[string]string {
	start := strings.Index(html, "<svg")
	end := strings.Index(html[max(start, 0):], ">")
	if start < 0 || end < 0 {
		return nil
	}
	return attributes(html[start : start+end])
}

func attributes(text string) map[string]string {
	list := map[string]string{}
	for _, match := range svgAttribute.FindAllStringSubmatch(text, -1) {
		list[match[1]] = match[2]
	}
	return list
}

// classStyles reads the properties of each .class rule in mascot.css.
func classStyles(css string) map[string]map[string]string {
	styles := map[string]map[string]string{}
	for _, rule := range cssRule.FindAllStringSubmatch(cssComment.ReplaceAllString(css, ""), -1) {
		for selector := range strings.SplitSeq(rule[1], ",") {
			class, ok := strings.CutPrefix(strings.TrimSpace(selector), ".")
			if !ok {
				continue
			}
			if styles[class] == nil {
				styles[class] = map[string]string{}
			}
			for declaration := range strings.SplitSeq(rule[2], ";") {
				name, value, ok := strings.Cut(declaration, ":")
				if ok {
					styles[class][strings.TrimSpace(name)] = strings.TrimSpace(value)
				}
			}
		}
	}
	return styles
}

func newShape(element string, attrs map[string]string, styles map[string]map[string]string, colors map[string]string) (shape, error) {
	s := shape{element: element}
	style, ok := styles[attrs["class"]]
	if !ok {
		return s, fmt.Errorf("mascot.css has no rule for class %q", attrs["class"])
	}
	var err error
	if s.fill, err = paint(style["fill"], colors); err != nil {
		return s, err
	}
	if s.stroke, err = paint(style["stroke"], colors); err != nil {
		return s, err
	}
	if s.stroke != nil {
		if s.strokeWidth, err = strconv.ParseFloat(style["stroke-width"], 64); err != nil {
			return s, fmt.Errorf("stroke-width %q is not a number", style["stroke-width"])
		}
		switch style["stroke-linecap"] {
		case "", "butt":
		case "round":
			s.roundCaps = true
		default:
			return s, fmt.Errorf("stroke-linecap %q is not supported; use butt or round", style["stroke-linecap"])
		}
	}

	number := func(name string) float64 {
		value, parseErr := strconv.ParseFloat(attrs[name], 64)
		if parseErr != nil && err == nil {
			err = fmt.Errorf("%s=%q is not a number", name, attrs[name])
		}
		return value
	}
	switch element {
	case "circle", "ellipse":
		cx, cy := number("cx"), number("cy")
		var rx, ry float64
		if element == "circle" {
			rx = number("r")
			ry = rx
		} else {
			rx, ry = number("rx"), number("ry")
		}
		if err != nil {
			return s, err
		}
		if s.stroke != nil {
			return s, fmt.Errorf("only paths can have a stroke here")
		}
		s.left, s.top, s.right, s.bottom = cx-rx, cy-ry, cx+rx, cy+ry
		s.inside = func(x, y float64) bool {
			dx, dy := (x-cx)/rx, (y-cy)/ry
			return dx*dx+dy*dy <= 1
		}
	case "path":
		if s.fill != nil {
			return s, fmt.Errorf("paths can only be stroked here; set fill: none")
		}
		if s.line, err = flatten(attrs["d"]); err != nil {
			return s, err
		}
		s.left, s.top, s.right, s.bottom = math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
		for _, point := range s.line {
			s.left, s.right = math.Min(s.left, point[0]), math.Max(s.right, point[0])
			s.top, s.bottom = math.Min(s.top, point[1]), math.Max(s.bottom, point[1])
		}
		half := s.strokeWidth / 2
		s.left, s.top, s.right, s.bottom = s.left-half, s.top-half, s.right+half, s.bottom+half
	}
	return s, nil
}

// paint turns a CSS fill or stroke into a color: a hex color, a
// var(--mascot-*) of the theme, or none.
func paint(value string, colors map[string]string) (*color.NRGBA, error) {
	if value == "" || value == "none" {
		return nil, nil
	}
	if name, ok := strings.CutPrefix(value, "var("); ok {
		name = strings.TrimSuffix(name, ")")
		if value = colors[name]; value == "" {
			return nil, fmt.Errorf("the built website has no %s color", name)
		}
	}
	var c color.NRGBA
	if _, err := fmt.Sscanf(strings.ToLower(value), "#%02x%02x%02x", &c.R, &c.G, &c.B); err != nil || len(value) != 7 {
		return nil, fmt.Errorf("color %q is not #rrggbb or var(--mascot-*)", value)
	}
	c.A = 0xff
	return &c, nil
}
