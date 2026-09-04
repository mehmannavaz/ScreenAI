package imaging

import (
	"fmt"
	"image"
)

// decodeTGA decodes true-color and paletted TGA images, with or without
// RLE compression, at 16/24/32 bpp (16 bpp is 5:5:5 RGB with a 1-bit
// alpha flag in the descriptor). Origin handling (top-down vs bottom-up
// rows) matches the TGA specification.
func decodeTGA(data []byte) (*image.RGBA, error) {
	if len(data) < 18 {
		return nil, fmt.Errorf("imaging: TGA header truncated")
	}
	idLen := int(data[0])
	colorMapType := data[1]
	imageType := data[2]
	colorMapFirst := int(data[3]) | int(data[4])<<8
	colorMapLen := int(data[5]) | int(data[6])<<8
	colorMapBPP := int(data[7])
	width := int(data[12]) | int(data[13])<<8
	height := int(data[14]) | int(data[15])<<8
	bpp := int(data[16])
	descriptor := data[17]

	pos := 18 + idLen
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("imaging: TGA has degenerate dimensions %dx%d", width, height)
	}

	// Color map (palette).
	var palette [][4]uint8
	if colorMapType == 1 {
		if colorMapBPP != 24 && colorMapBPP != 32 {
			return nil, fmt.Errorf("imaging: TGA color map bpp %d not supported", colorMapBPP)
		}
		entryBytes := colorMapBPP / 8
		need := pos + colorMapLen*entryBytes
		if need > len(data) {
			return nil, fmt.Errorf("imaging: TGA color map truncated")
		}
		for i := 0; i < colorMapLen; i++ {
			off := pos + i*entryBytes
			b, g, r, a := data[off], data[off+1], data[off+2], uint8(255)
			if entryBytes == 4 {
				a = data[off+3]
			}
			palette = append(palette, [4]uint8{r, g, b, a})
		}
		pos = need
	}

	var trueColorBits int
	switch imageType {
	case 1, 9: // colormapped, plain or RLE
		if colorMapType != 1 {
			return nil, fmt.Errorf("imaging: colormapped TGA without color map")
		}
		trueColorBits = 8
	case 2, 10: // true-color, plain or RLE
		trueColorBits = bpp
	default:
		return nil, fmt.Errorf("imaging: TGA image type %d not supported", imageType)
	}
	if trueColorBits != 8 && trueColorBits != 16 && trueColorBits != 24 && trueColorBits != 32 {
		return nil, fmt.Errorf("imaging: TGA bpp %d not supported", bpp)
	}
	rle := imageType == 9 || imageType == 10
	pixelBytes := trueColorBits / 8

	readPixel := func(off int) ([4]uint8, error) {
		var px [4]uint8
		switch pixelBytes {
		case 1:
			idx := int(data[off])
			idx -= colorMapFirst
			if idx < 0 || idx >= len(palette) {
				idx = 0
			}
			px = palette[idx]
		case 2:
			v := uint16(data[off]) | uint16(data[off+1])<<8
			// 5:5:5 RGB; bit 15 is an alpha attribute bit.
			px[0] = uint8((v & 0x7c00) >> 7)
			px[1] = uint8((v & 0x03e0) >> 2)
			px[2] = uint8((v & 0x001f) << 3)
			px[3] = 255
			if descriptor&0x08 != 0 {
				px[3] = 0
			}
		case 3:
			px = [4]uint8{data[off+2], data[off+1], data[off], 255}
		case 4:
			px = [4]uint8{data[off+2], data[off+1], data[off], data[off+3]}
		}
		return px, nil
	}

	out := image.NewRGBA(image.Rect(0, 0, width, height))
	put := func(x, y int, px [4]uint8) {
		// Row order: bit 5 of the descriptor set = top-down.
		if descriptor&0x20 == 0 {
			y = height - 1 - y
		}
		i := y*out.Stride + x*4
		out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = px[0], px[1], px[2], px[3]
	}

	if !rle {
		need := pos + width*height*pixelBytes
		if need > len(data) {
			return nil, fmt.Errorf("imaging: TGA pixel data truncated")
		}
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				px, _ := readPixel(pos + (y*width+x)*pixelBytes)
				put(x, y, px)
			}
		}
		return out, nil
	}

	// RLE: packets of up to 128 pixels; count bit 7 set = run of one
	// repeated pixel, otherwise raw pixel values.
	x, y := 0, 0
	total := width * height
	written := 0
	for written < total {
		if pos >= len(data) {
			return nil, fmt.Errorf("imaging: TGA RLE data truncated")
		}
		count := int(data[pos]) & 0x7f
		isRun := data[pos]&0x80 != 0
		pos++
		if count == 0 {
			continue
		}
		if isRun {
			if pos+pixelBytes > len(data) {
				return nil, fmt.Errorf("imaging: TGA RLE run truncated")
			}
			px, _ := readPixel(pos)
			pos += pixelBytes
			for i := 0; i < count && written < total; i++ {
				put(x, y, px)
				x++
				if x == width {
					x, y = 0, y+1
				}
				written++
			}
		} else {
			if pos+count*pixelBytes > len(data) {
				return nil, fmt.Errorf("imaging: TGA RLE raw packet truncated")
			}
			for i := 0; i < count && written < total; i++ {
				px, _ := readPixel(pos)
				pos += pixelBytes
				put(x, y, px)
				x++
				if x == width {
					x, y = 0, y+1
				}
				written++
			}
		}
	}
	return out, nil
}
