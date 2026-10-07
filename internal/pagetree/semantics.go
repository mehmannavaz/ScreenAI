// semantics.go holds the semantic-tree knobs and detectors shared by
// the OCR and text tree builders.
//
// Deep analysis of the Screen2x model config (screen2x_config.pbtxt)
// shows the model is a message-passing GNN whose per-node inputs are
// numeric features PLUS a dedicated categorical "chrome_role" input
// and a "chrome_role_description" string. Chrome's own trees always
// populate rich roles (heading with hierarchical_level, link, list,
// listItem, banner, ...), so a wrapper that feeds the model nothing
// but rootWebArea/paragraph/staticText starves its main structural
// feature. These detectors reconstruct those roles from the signals
// the OCR annotation itself carries: line heights, boxes, per-word
// foreground colors (packed 0xRRGGBB00) and the image pixels.
package pagetree

import (
	"image"
	"regexp"
	"sort"

	"github.com/mehmannavaz/ScreenAI/internal/pb"
)

// TreeOptions are the semantic-tree knobs (settings group "tree.*").
// They control how much structure the wrapper infers from the OCR
// output before the tree is handed to the Screen2x model.
type TreeOptions struct {
	// DetectHeadings marks clearly larger lines as role "heading"
	// with an estimated hierarchical_level (tree.detect_headings).
	DetectHeadings bool
	// DetectLists groups bulleted/numbered lines into
	// list/listItem nodes, and horizontal runs of short same-row
	// lines (nav bars, footers) into lists as well
	// (tree.detect_lists).
	DetectLists bool
	// DetectLinks marks lines whose word ink is link-coloured — or
	// that carry a solid underline, when the pixels are available —
	// as role "link" (tree.detect_links).
	DetectLinks bool
	// DetectRegions wraps side columns in role "complementary"
	// (aside) and the trailing footer strip in role "contentinfo"
	// (tree.detect_regions) — the landmark roles Chrome's trees carry
	// and classic clutter signals for Screen2x.
	DetectRegions bool
	// HeadingMinRatio is the height ratio over the body text below
	// which a line is never a heading (tree.heading_min_ratio).
	HeadingMinRatio float64
	// ParagraphGap is the maximum vertical gap between two lines,
	// in units of the body line height, that still merges them into
	// one paragraph node (tree.paragraph_gap).
	ParagraphGap float64
}

// DefaultTreeOptions returns the stock semantic-tree settings: every
// detector on, a heading needs to be 1.3× the body size, and lines
// closer than 0.75 body heights merge into one paragraph.
func DefaultTreeOptions() TreeOptions {
	return TreeOptions{
		DetectHeadings:  true,
		DetectLists:     true,
		DetectLinks:     true,
		DetectRegions:   true,
		HeadingMinRatio: 1.3,
		ParagraphGap:    0.75,
	}
}

// normalize repairs zero fields to their defaults so a partially-set
// TreeOptions still behaves.
func (o TreeOptions) normalize() TreeOptions {
	if o.HeadingMinRatio <= 0 {
		o.HeadingMinRatio = 1.3
	}
	if o.ParagraphGap <= 0 {
		o.ParagraphGap = 0.75
	}
	return o
}

// listMarkerRe matches the list-item prefixes OCR text commonly
// carries: bullets (• - * ...), decimal (1. 2)), letter (a. b)) and
// roman (i. iv.) markers. A marker must be followed by whitespace so
// "3.5 million" or "-5" never match.
var listMarkerRe = regexp.MustCompile(`^(?:[•●▪◦‣∙·]|[-*–—]|\(\d{1,3}\)|\d{1,3}[.)]|[a-zA-Z][.)]|[ivxIVX]{1,5}[.)])\s`)

// isListItem reports whether an OCR line starts with a list marker.
func isListItem(text string) bool { return listMarkerRe.MatchString(text) }

// headingLevel maps a height ratio over body text to a
// hierarchical_level 1..6, following the classic type scale (a 2×
// headline is an h1, 1.6× an h2, and so on).
func headingLevel(ratio float64) int {
	switch {
	case ratio >= 1.9:
		return 1
	case ratio >= 1.6:
		return 2
	case ratio >= 1.45:
		return 3
	case ratio >= 1.35:
		return 4
	default:
		return 5
	}
}

// isLinkInk reports whether a text colour looks like a hyperlink:
// link blue (#1a0dab, #1155cc, #1a73e8, ...), visited-link purple
// (#660099) or a teal link. The test is blue/purple dominance over
// both red and green, which body grays and blacks never pass.
func isLinkInk(r, g, b int) bool {
	return b >= 80 && b >= r+50 && b >= g+40
}

// wordInk returns the dominant foreground colour of a line from the
// engine's own per-word foreground_rgb values (packed 0xRRGGBB00).
// ok is false when no word reports a colour.
func wordInk(ln *pb.Line) (r, g, b int, ok bool) {
	counts := map[[3]int]int{}
	for i := range ln.Words {
		if ln.Words[i].ForegroundRGB == nil {
			continue
		}
		v := int(*ln.Words[i].ForegroundRGB)
		c := [3]int{v >> 24 & 0xFF, v >> 16 & 0xFF, v >> 8 & 0xFF}
		counts[c]++
	}
	best, n := [3]int{}, 0
	for c, k := range counts {
		if k > n {
			best, n = c, k
		}
	}
	if n == 0 {
		return 0, 0, 0, false
	}
	return best[0], best[1], best[2], true
}

// inkAnalyzer decides which lines look like hyperlinks, combining
// the engine's per-word ink colours with (optional) pixel inspection
// for solid underlines.
type inkAnalyzer struct {
	img *image.RGBA // may be nil: no underline detection then
}

// lineLinkish reports whether a line reads as a link. Heading-sized
// lines never do — a blue headline is still a heading.
func (a *inkAnalyzer) lineLinkish(ln *pb.Line, headingSize bool) bool {
	if headingSize {
		return false
	}
	if r, g, b, ok := wordInk(ln); ok && isLinkInk(r, g, b) {
		return true
	}
	if a.img != nil && ln.Box != nil && ln.Box.Height >= 10 {
		if hasSolidUnderline(a.img, ln.Box) {
			return true
		}
	}
	return false
}

// hasSolidUnderline looks for a near-continuous horizontal run of
// ink in the lower part of the line box — what an underlined link
// draws, and what a row of ordinary glyphs never produces (letters
// leave gaps between words).
func hasSolidUnderline(img *image.RGBA, box *pb.Rect) bool {
	x0, y0 := box.X, box.Y
	x1, y1 := x0+box.Width, y0+box.Height
	b := img.Bounds()
	if x1 <= x0 || y1 <= y0 {
		return false
	}
	x0, y0 = max(x0, b.Min.X), max(y0, b.Min.Y)
	x1, y1 = min(x1, b.Max.X), min(y1, b.Max.Y)
	if x1-x0 < 8 || y1-y0 < 8 {
		return false
	}
	// rows from 68% to 95% of the box height
	for y := y0 + (y1-y0)*68/100; y <= y0+(y1-y0)*95/100; y++ {
		run, best := 0, 0
		for x := x0; x < x1; x++ {
			if isInkPixel(img, x, y) {
				run++
			} else {
				run = 0
			}
			if run > best {
				best = run
			}
		}
		if best >= (x1-x0)*7/10 {
			return true
		}
	}
	return false
}

// isInkPixel reports whether a pixel is clearly darker than white-ish
// paper. Underlines are drawn dark on light backgrounds in practice;
// dark-mode screenshots are not supported by this heuristic.
func isInkPixel(img *image.RGBA, x, y int) bool {
	off := (y-img.Bounds().Min.Y)*img.Stride + (x-img.Bounds().Min.X)*4
	pix := img.Pix
	r, g, b := int(pix[off]), int(pix[off+1]), int(pix[off+2])
	return r+g+b < 480 // darker than a ~160 gray average
}

// bodyHeight returns the area-weighted median OCR line height — the
// body text size. Weighting by box area (not line count) is essential:
// on a real page five short 15px nav lines must not outweigh ten long
// 21px body lines, or every body paragraph would be mistaken for a
// heading. Weighting by area also matches what the model "sees".
func bodyHeight(lines []*pb.Line) float64 {
	type wh struct{ h, area float64 }
	var items []wh
	total := 0.0
	for _, ln := range lines {
		if ln.Box == nil || ln.Box.Height <= 0 {
			continue
		}
		area := float64(ln.Box.Width) * float64(ln.Box.Height)
		if area <= 0 {
			area = 1
		}
		items = append(items, wh{float64(ln.Box.Height), area})
		total += area
	}
	if len(items) == 0 || total <= 0 {
		return 16
	}
	sort.Slice(items, func(i, j int) bool { return items[i].h < items[j].h })
	half := total / 2
	acc := 0.0
	for _, it := range items {
		acc += it.area
		if acc >= half {
			return it.h
		}
	}
	return items[len(items)-1].h
}

// fontPxFromHeight estimates the CSS font size from an OCR line box
// height: the box includes ascenders and descenders, which cover
// roughly 0.78 of the em box for typical text.
func fontPxFromHeight(h float64) float64 {
	return max(8.0, float64(round64(h*0.78)))
}

func round64(v float64) int {
	if v < 0 {
		return int(v - 0.5)
	}
	return int(v + 0.5)
}
