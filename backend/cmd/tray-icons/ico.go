package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
)

// encodeICO writes a Windows icon file with one image per size, as 32-bit
// bitmaps with alpha, which every Windows version reads.
func encodeICO(sizes []int, draw func(size int) *image.NRGBA) ([]byte, error) {
	var header, images bytes.Buffer
	write := func(buffer *bytes.Buffer, values ...any) {
		for _, value := range values {
			// Writing to a bytes.Buffer does not fail.
			_ = binary.Write(buffer, binary.LittleEndian, value)
		}
	}
	if len(sizes) > 255 {
		return nil, fmt.Errorf("%d sizes are more than an icon holds", len(sizes))
	}
	write(&header, uint16(0), uint16(1), uint16(len(sizes))) //nolint:gosec // checked above
	offset := 6 + 16*len(sizes)
	for _, size := range sizes {
		if size < 1 || size > 255 {
			return nil, fmt.Errorf("size %d is not 1 to 255 pixels", size)
		}
		img := draw(size)
		start := images.Len()
		// BITMAPINFOHEADER; the height counts the color rows and the mask.
		write(&images, uint32(40), int32(size), int32(2*size), uint16(1), uint16(32),
			uint32(0), uint32(0), int32(0), int32(0), uint32(0), uint32(0))
		// Colors as blue, green, red, alpha, from the bottom row up.
		for y := size - 1; y >= 0; y-- {
			for x := range size {
				c := img.NRGBAAt(x, y)
				images.Write([]byte{c.B, c.G, c.R, c.A})
			}
		}
		// The 1-bit mask is unused next to the alpha, but must be there:
		// rows of 32-bit words, all zero.
		images.Write(make([]byte, (size+31)/32*4*size))
		length := images.Len() - start
		write(&header, uint8(size), uint8(size), uint8(0), uint8(0), uint16(1), uint16(32),
			uint32(length), uint32(offset+start)) //nolint:gosec // a few hundred kilobytes at most
	}
	return append(header.Bytes(), images.Bytes()...), nil
}
