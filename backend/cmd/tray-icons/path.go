package main

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
)

var pathToken = regexp.MustCompile(`[a-zA-Z]|[-+]?(?:\d+\.?\d*|\.\d+)(?:[eE][-+]?\d+)?`)

// arcSegments is how many straight pieces an arc is drawn with; plenty for
// icons up to 64 pixels.
const arcSegments = 32

// flatten turns an SVG path of M, L, H, V and A commands into points joined
// by straight lines.
func flatten(d string) ([][2]float64, error) {
	tokens := pathToken.FindAllString(d, -1)
	var (
		points  [][2]float64
		command string
		x, y    float64
	)
	next := func() (float64, error) {
		if len(tokens) == 0 {
			return 0, fmt.Errorf("path %q ends in the middle of %s", d, command)
		}
		value, err := strconv.ParseFloat(tokens[0], 64)
		if err != nil {
			return 0, fmt.Errorf("path %q: %q is not a number", d, tokens[0])
		}
		tokens = tokens[1:]
		return value, nil
	}
	for len(tokens) > 0 {
		if _, err := strconv.ParseFloat(tokens[0], 64); err != nil {
			command, tokens = tokens[0], tokens[1:]
		} else if command == "" {
			return nil, fmt.Errorf("path %q must start with M", d)
		}
		relative := command[0] >= 'a'
		var ox, oy float64
		if relative {
			ox, oy = x, y
		}
		switch command {
		case "M", "m", "L", "l":
			nx, err := next()
			if err != nil {
				return nil, err
			}
			ny, err := next()
			if err != nil {
				return nil, err
			}
			if (command == "M" || command == "m") && len(points) > 0 {
				return nil, fmt.Errorf("path %q: only one M at the start is supported", d)
			}
			x, y = ox+nx, oy+ny
			points = append(points, [2]float64{x, y})
			// More numbers after M draw lines, as in SVG.
			switch command {
			case "M":
				command = "L"
			case "m":
				command = "l"
			}
		case "H", "h":
			nx, err := next()
			if err != nil {
				return nil, err
			}
			x = ox + nx
			points = append(points, [2]float64{x, y})
		case "V", "v":
			ny, err := next()
			if err != nil {
				return nil, err
			}
			y = oy + ny
			points = append(points, [2]float64{x, y})
		case "A", "a":
			var values [7]float64
			for i := range values {
				value, err := next()
				if err != nil {
					return nil, err
				}
				values[i] = value
			}
			ex, ey := ox+values[5], oy+values[6]
			points = append(points, arc(x, y, values[0], values[1], values[2], values[3] != 0, values[4] != 0, ex, ey)...)
			x, y = ex, ey
		default:
			return nil, fmt.Errorf("path %q: command %s is not supported; use M, L, H, V or A", d, command)
		}
		if len(points) == 0 {
			return nil, fmt.Errorf("path %q must start with M", d)
		}
	}
	if len(points) < 2 {
		return nil, fmt.Errorf("path %q draws nothing", d)
	}
	return points, nil
}

// arc returns the points of an SVG elliptical arc from (x1, y1) to (x2, y2),
// without the start point, following the SVG specification's conversion to
// a center and angles.
func arc(x1, y1, rx, ry, rotation float64, large, sweep bool, x2, y2 float64) [][2]float64 {
	rx, ry = math.Abs(rx), math.Abs(ry)
	if rx == 0 || ry == 0 {
		return [][2]float64{{x2, y2}}
	}
	phi := rotation * math.Pi / 180
	cos, sin := math.Cos(phi), math.Sin(phi)
	dx, dy := (x1-x2)/2, (y1-y2)/2
	px, py := cos*dx+sin*dy, -sin*dx+cos*dy
	// Radii too small to reach the end point are scaled up, as browsers do.
	if scale := px*px/(rx*rx) + py*py/(ry*ry); scale > 1 {
		rx, ry = rx*math.Sqrt(scale), ry*math.Sqrt(scale)
	}
	factor := math.Sqrt(math.Max(0, (rx*rx*ry*ry-rx*rx*py*py-ry*ry*px*px)/(rx*rx*py*py+ry*ry*px*px)))
	if large == sweep {
		factor = -factor
	}
	cxp, cyp := factor*rx*py/ry, -factor*ry*px/rx
	cx, cy := cos*cxp-sin*cyp+(x1+x2)/2, sin*cxp+cos*cyp+(y1+y2)/2

	start := math.Atan2((py-cyp)/ry, (px-cxp)/rx)
	delta := math.Atan2((-py-cyp)/ry, (-px-cxp)/rx) - start
	if sweep && delta < 0 {
		delta += 2 * math.Pi
	} else if !sweep && delta > 0 {
		delta -= 2 * math.Pi
	}
	points := make([][2]float64, 0, arcSegments)
	for i := 1; i <= arcSegments; i++ {
		angle := start + delta*float64(i)/arcSegments
		ax, ay := rx*math.Cos(angle), ry*math.Sin(angle)
		points = append(points, [2]float64{cos*ax - sin*ay + cx, sin*ax + cos*ay + cy})
	}
	return points
}
