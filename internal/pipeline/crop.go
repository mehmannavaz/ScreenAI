package pipeline

import (
	"fmt"
	"image"

	"github.com/mehmannavaz/ScreenAI/internal/imaging"
)

// CropPadding grows the main-content crop box by this many pixels on
// each side (clamped to the image) so ascenders/descenders at region
// edges are not shaved off. Purely cosmetic; the JSON crop_box
// reports the model's exact union box.
const CropPadding = 4

// CropMainContent crops the union box of the Screen2x-selected nodes
// out of img — the image the OCR actually saw, so box coordinates
// line up exactly — and encodes it as a genuine PNG. m must carry a
// CropBox (computed by RunMCE); inputs without a selection cannot be
// cropped.
func CropMainContent(img *image.RGBA, m *MCE) ([]byte, error) {
	if m == nil || m.CropBox == nil {
		return nil, fmt.Errorf("no main-content selection to crop")
	}
	if img == nil {
		return nil, fmt.Errorf("no image to crop")
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	l := m.CropBox[0] - CropPadding
	t := m.CropBox[1] - CropPadding
	r := m.CropBox[2] + CropPadding
	b := m.CropBox[3] + CropPadding
	l, t = clampInt(l, 0, w), clampInt(t, 0, h)
	r, b = clampInt(r, 0, w), clampInt(b, 0, h)
	if r-l < 1 || b-t < 1 {
		return nil, fmt.Errorf("main-content crop box is empty")
	}
	sub := img.SubImage(image.Rect(l, t, r, b)).(*image.RGBA)
	return imaging.EncodePNG(sub)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
