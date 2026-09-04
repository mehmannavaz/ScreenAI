// Package imaging provides universal raster decoding, area-averaging
// downscale, and format sniffing so that any common image format can go
// straight into the Screen AI OCR.
//
// Supported formats:
//
//	PNG   via image/png (all bit depths, gray / rgb / palette / alpha, Adam7)
//	JPEG  via image/jpeg (baseline + extended; progressive is rejected by
//	      the decoder with a clear error)
//	GIF   via image/gif (LZW, local/global palettes, first frame)
//	BMP   via golang.org/x/image/bmp (1/4/8/24/32 bpp, palettes)
//	TGA   custom decoder (true-color and paletted, RLE, 16/24/32 bpp)
//	PPM   custom decoder for the netpbm family (PBM/PGM/PPM, ASCII+binary)
//
// Whatever the input format was, the pipeline always re-encodes what OCR
// saw as a genuine PNG for previews and reports.
package imaging

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"

	"golang.org/x/image/bmp"
)

// ErrUnsupportedFormat reports input bytes that no decoder recognizes.
var ErrUnsupportedFormat = fmt.Errorf("imaging: unrecognized image format (supported: PNG, JPEG, GIF, BMP, TGA, PPM/PGM/PBM); for WebP or progressive JPEG convert to PNG first")

// Sniff returns the format name ("png", "jpeg", "gif", "bmp", "tga",
// "ppm") detected from magic bytes (plus a filename hint for TGA, which
// has no magic at the start), or "" when unknown.
func Sniff(data []byte, hint string) string {
	switch {
	case hasPrefix(data, "\x89PNG\r\n\x1a\n"):
		return "png"
	case hasPrefix(data, "\xff\xd8"):
		return "jpeg"
	case hasPrefix(data, "GIF87a"), hasPrefix(data, "GIF89a"):
		return "gif"
	case hasPrefix(data, "BM"):
		return "bmp"
	}
	if len(data) > 2 && data[0] == 'P' && data[1] >= '1' && data[1] <= '6' &&
		(len(data) < 3 || data[2] == ' ' || data[2] == '\t' || data[2] == '\r' || data[2] == '\n') {
		return "ppm"
	}
	// TGA has no magic at the start; check the footer, then the extension.
	if len(data) >= 18 && string(data[len(data)-18:]) == "TRUEVISION-XFILE.\x00" {
		return "tga"
	}
	lower := toLowerASCII(hint)
	if endsWith(lower, ".tga") || endsWith(lower, ".tpic") {
		return "tga"
	}
	return ""
}

func toLowerASCII(s string) string {
	out := []byte(s)
	for i, b := range out {
		if b >= 'A' && b <= 'Z' {
			out[i] = b + 'a' - 'A'
		}
	}
	return string(out)
}

func hasPrefix(b []byte, prefix string) bool {
	return len(b) >= len(prefix) && string(b[:len(prefix)]) == prefix
}

func endsWith(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

// Decode decodes data into a tightly packed RGBA image. hint is the file
// name (used only for TGA, which lacks a magic).
func Decode(data []byte, hint string) (*image.RGBA, error) {
	switch Sniff(data, hint) {
	case "png":
		return decodeWith(png.Decode, data)
	case "jpeg":
		return decodeWith(jpeg.Decode, data)
	case "gif":
		return decodeWith(gif.Decode, data)
	case "bmp":
		return decodeWith(bmp.Decode, data)
	case "ppm":
		return decodePPM(data)
	case "tga":
		return decodeTGA(data)
	}
	return nil, ErrUnsupportedFormat
}

func decodeWith(decode func(io.Reader) (image.Image, error), data []byte) (*image.RGBA, error) {
	img, err := decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("imaging: %w", err)
	}
	return ToRGBA(img), nil
}

// ToRGBA converts any image to a tightly packed *image.RGBA (stride ==
// width*4) — the layout PerformOCR expects.
func ToRGBA(img image.Image) *image.RGBA {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if rgba, ok := img.(*image.RGBA); ok && rgba.Stride == w*4 && b.Min == (image.Point{}) {
		return rgba
	}
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// Fast path for common concrete types.
			switch src := img.(type) {
			case *image.RGBA:
				if b.Min == (image.Point{}) {
					i := y*src.Stride + x*4
					out.Pix[y*out.Stride+x*4+0] = src.Pix[i]
					out.Pix[y*out.Stride+x*4+1] = src.Pix[i+1]
					out.Pix[y*out.Stride+x*4+2] = src.Pix[i+2]
					out.Pix[y*out.Stride+x*4+3] = src.Pix[i+3]
					continue
				}
			case *image.NRGBA:
				i := (y+b.Min.Y)*src.Stride + (x+b.Min.X)*4
				copy(out.Pix[y*out.Stride+x*4:y*out.Stride+x*4+4], src.Pix[i:i+4])
				continue
			}
			r, g, bb, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			out.SetRGBA(x, y, colorRGBA(r, g, bb, a))
		}
	}
	return out
}

func colorRGBA(r, g, b, a uint32) color.RGBA {
	return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
}
