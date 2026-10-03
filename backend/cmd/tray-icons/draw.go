package main

import (
	"image"
	"image/color"
	"math"
)

// samples is how many points per pixel, in each direction, are tested to
// smooth the edges.
const samples = 8

// The status dot sits over the bear's lower right, with a white ring that
// keeps it apart from the fur. Sizes are in the mascot's 32 by 32 units.
const (
	dotX, dotY = 25.0, 25.0
	dotRadius  = 6.0
	dotRing    = 1.5
)

// render draws the bear at size by size pixels, with a status dot in the
// given color, or none when it is nil.
func (d *drawing) render(size int, status *color.NRGBA) *image.NRGBA {
	shapes := d.shapes
	if status != nil {
		white := color.NRGBA{0xff, 0xff, 0xff, 0xff}
		shapes = append(shapes[:len(shapes):len(shapes)],
			disc(dotX/32*d.width, dotY/32*d.height, (dotRadius+dotRing)/32*d.width, white),
			disc(dotX/32*d.width, dotY/32*d.height, dotRadius/32*d.width, *status))
	}
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	scaleX, scaleY := d.width/float64(size), d.height/float64(size)
	for py := range size {
		for px := range size {
			// Premultiplied red, green, blue and alpha, from 0 to 1.
			var r, g, b, a float64
			left, top := float64(px)*scaleX, float64(py)*scaleY
			for _, s := range shapes {
				if left > s.right || left+scaleX < s.left || top > s.bottom || top+scaleY < s.top {
					continue
				}
				for _, layer := range []struct {
					c  *color.NRGBA
					in func(x, y float64) bool
				}{{s.fill, s.inside}, {s.stroke, s.onStroke}} {
					if layer.c == nil || layer.in == nil {
						continue
					}
					covered := 0
					for sy := range samples {
						for sx := range samples {
							x := (float64(px) + (float64(sx)+0.5)/samples) * scaleX
							y := (float64(py) + (float64(sy)+0.5)/samples) * scaleY
							if layer.in(x, y) {
								covered++
							}
						}
					}
					alpha := float64(covered) / (samples * samples)
					r = r*(1-alpha) + float64(layer.c.R)/255*alpha
					g = g*(1-alpha) + float64(layer.c.G)/255*alpha
					b = b*(1-alpha) + float64(layer.c.B)/255*alpha
					a = a*(1-alpha) + alpha
				}
			}
			if a > 0 {
				img.SetNRGBA(px, py, color.NRGBA{channel(r / a), channel(g / a), channel(b / a), channel(a)})
			}
		}
	}
	return img
}

func channel(value float64) uint8 {
	return uint8(math.Round(math.Min(1, math.Max(0, value)) * 255))
}

// disc is a filled circle.
func disc(cx, cy, radius float64, c color.NRGBA) shape {
	return shape{
		element: "circle", fill: &c,
		inside: func(x, y float64) bool {
			return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= radius*radius
		},
		left: cx - radius, top: cy - radius, right: cx + radius, bottom: cy + radius,
	}
}

// onStroke tells whether a point is on a path's stroke. Butt caps end the
// stroke at the first and last point; round caps go half its width further.
func (s shape) onStroke(x, y float64) bool {
	half := s.strokeWidth / 2
	last := len(s.line) - 2
	for i := 0; i < len(s.line)-1; i++ {
		ax, ay := s.line[i][0], s.line[i][1]
		vx, vy := s.line[i+1][0]-ax, s.line[i+1][1]-ay
		t := 0.0
		if length := vx*vx + vy*vy; length > 0 {
			t = ((x-ax)*vx + (y-ay)*vy) / length
		}
		if !s.roundCaps && ((i == 0 && t < 0) || (i == last && t > 1)) {
			continue
		}
		t = math.Min(1, math.Max(0, t))
		if math.Hypot(x-(ax+t*vx), y-(ay+t*vy)) <= half {
			return true
		}
	}
	return false
}
