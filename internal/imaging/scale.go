package imaging

import (
	"bytes"
	"image"
	"image/png"
)

// ResizeMax area-average downscales img so that max(width, height) <=
// maxDim, returning the original when it already fits.
//
// Box filtering (not nearest-neighbour) keeps thin glyph strokes usable
// for OCR after a large downscale.
func ResizeMax(img *image.RGBA, maxDim int) *image.RGBA {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if w <= maxDim && h <= maxDim || maxDim <= 0 {
		return img
	}
	m := w
	if h > m {
		m = h
	}
	scale := float64(maxDim) / float64(m)
	newW := max(1, int(float64(w)*scale+0.5))
	newH := max(1, int(float64(h)*scale+0.5))
	if newW > maxDim {
		newW = maxDim
	}
	if newH > maxDim {
		newH = maxDim
	}

	src := img.Pix
	stride := img.Stride
	out := image.NewRGBA(image.Rect(0, 0, newW, newH))
	for ny := 0; ny < newH; ny++ {
		y0 := ny * h / newH
		y1 := (ny + 1) * h / newH
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for nx := 0; nx < newW; nx++ {
			x0 := nx * w / newW
			x1 := (nx + 1) * w / newW
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, b, a, n uint64
			for y := y0; y < y1; y++ {
				irow := y * stride
				for x := x0; x < x1; x++ {
					i := irow + x*4
					r += uint64(src[i])
					g += uint64(src[i+1])
					b += uint64(src[i+2])
					a += uint64(src[i+3])
					n++
				}
			}
			o := ny*out.Stride + nx*4
			out.Pix[o] = uint8(r / n)
			out.Pix[o+1] = uint8(g / n)
			out.Pix[o+2] = uint8(b / n)
			out.Pix[o+3] = uint8(a / n)
		}
	}
	return out
}

// EncodePNG encodes img as a genuine 8-bit RGBA PNG — whatever the input
// format was (BMP/JPEG/GIF/TGA/PPM...), previews and HTTP responses
// always carry a real PNG.
func EncodePNG(img *image.RGBA) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
