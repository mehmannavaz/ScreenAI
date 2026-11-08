// Package pdftext extracts the text layer of PDF documents with exact
// geometry and turns every page into the same pb.Annotation structure
// the OCR pipeline produces — so PDF pages flow through the identical
// Screen2x main-content pipeline (semantic tree, reader view, outline,
// stats, reports) without needing a single pixel of rasterization.
//
// The text layer of a PDF is what a PDF viewer renders its text from:
// per-glyph origins, baselines and font sizes in page points. It is
// exact — unlike OCR there is no recognition error — so every
// synthesized line carries confidence 1.0.
//
// Pages without a text layer (scanned PDFs: one big image per page)
// cannot be handled this way; the pipeline reports them explicitly so
// the user can rasterize those pages and run the real OCR path instead.
//
// The parser is github.com/ledongthuc/pdf (pure Go, no cgo — the binary
// stays a single static executable, CGO_ENABLED=0 like every other
// build of this project). PDFs encrypted with an empty user password
// (the common "protected but printable" case) are opened transparently;
// real passwords are reported as an error.
package pdftext

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"unicode"

	lpdf "github.com/ledongthuc/pdf"

	"github.com/mehmannavaz/ScreenAI/internal/pb"
)

// Page is one PDF page with its text layer converted to an OCR-style
// annotation. Boxes are in page points with a top-left origin — the
// coordinate system the rest of the pipeline uses for image pixels, so
// ratios (the only thing the Screen2x features are built from) carry
// over unchanged.
type Page struct {
	Number int // 1-based page number
	Width  int // page width in points
	Height int // page height in points
	Chunks int // raw text-layer glyphs found (0 = image-only page)
	Ann    *pb.Annotation
}

// Doc is a parsed PDF document.
type Doc struct {
	Pages []Page
	Name  string
}

// Parse reads the text layer of every page of data. name is used in
// error messages only.
func Parse(data []byte, name string) (*Doc, error) {
	r, err := lpdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		// Many "encrypted" PDFs use an empty user password (printing
		// and copying allowed, opening passwordless). Retry once with
		// the empty password before giving up.
		r, err = lpdf.NewReaderEncrypted(bytes.NewReader(data), int64(len(data)), func() string { return "" })
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	}
	doc := &Doc{Name: name, Pages: make([]Page, 0, r.NumPage())}
	for n := 1; n <= r.NumPage(); n++ {
		p := r.Page(n)
		if p.V.IsNull() {
			return nil, fmt.Errorf("%s: page %d not found", name, n)
		}
		x0, y0, x1, y1 := mediaBox(p)
		content := pageContent(p) // panic-guarded: a malformed content stream degrades to an empty page
		ann := annotation(content.Text, x0, y1, int(x1-x0), int(y1-y0))
		ann.Source = fmt.Sprintf("%s p%d/%d", name, n, r.NumPage())
		doc.Pages = append(doc.Pages, Page{
			Number: n,
			Width:  int(x1 - x0),
			Height: int(y1 - y0),
			Chunks: len(content.Text),
			Ann:    ann,
		})
	}
	return doc, nil
}

// mediaBox returns the page box (x0 y0 x1 y1) in points, walking up the
// page tree like the library's own (currently disabled) MediaBox
// accessor, and falling back to US Letter when the PDF carries none.
func mediaBox(p lpdf.Page) (x0, y0, x1, y1 float64) {
	for v := p.V; !v.IsNull(); v = v.Key("Parent") {
		mb := v.Key("MediaBox")
		if mb.Kind() == lpdf.Array && mb.Len() == 4 {
			v0, v1, v2, v3 := mb.Index(0).Float64(), mb.Index(1).Float64(), mb.Index(2).Float64(), mb.Index(3).Float64()
			if v2 > v0 && v3 > v1 {
				return v0, v1, v2, v3
			}
		}
	}
	return 0, 0, 612, 792
}

// pageContent returns the parsed content of p; a panic from a malformed
// content stream (the library parses panically) degrades to empty
// content instead of killing the whole run.
func pageContent(p lpdf.Page) (c lpdf.Content) {
	defer func() { _ = recover() }()
	return p.Content()
}

// glyph is one text-layer glyph, already flipped into top-left screen
// coordinates.
type glyph struct {
	x, w, right, top, size float64 // top = pageTop - baseline - size
	s                      string
}

// annotation clusters the per-glyph text-layer records into lines and
// words and builds the OCR-style annotation. x0/pageTop convert PDF
// user space (origin bottom-left) into the pipeline's top-left space.
func annotation(texts []lpdf.Text, x0, pageTop float64, pageW, pageH int) *pb.Annotation {
	ann := &pb.Annotation{Lines: []pb.Line{}, Width: pageW, Height: pageH}

	// Keep whitespace glyphs for now: the repair pass needs their
	// advances to keep words apart.
	glyphs := make([]glyph, 0, len(texts))
	for _, t := range texts {
		if t.FontSize <= 0 {
			t.FontSize = 10 // defensive: unsized glyphs still line up
		}
		glyphs = append(glyphs, glyph{
			x:     t.X - x0,
			w:     t.W,
			right: t.X - x0 + t.W,
			top:   pageTop - t.Y - t.FontSize,
			size:  t.FontSize,
			s:     t.S,
		})
	}
	repairCollapsed(glyphs)

	// Spacing lives in the geometry now — drop the invisible glyphs.
	ink := glyphs[:0]
	for _, g := range glyphs {
		if strings.TrimSpace(g.s) != "" {
			ink = append(ink, g)
		}
	}
	glyphs = ink
	if len(glyphs) == 0 {
		return ann
	}

	// Cluster baselines into rows: sort top-first (reading order), then
	// start a new row whenever the baseline moves more than half a line
	// height — superscripts and subscripts stay on their line.
	sort.SliceStable(glyphs, func(i, j int) bool {
		if glyphs[i].top != glyphs[j].top {
			return glyphs[i].top < glyphs[j].top
		}
		return glyphs[i].x < glyphs[j].x
	})
	var rows [][]glyph
	refTop, refSize := glyphs[0].top, glyphs[0].size
	for _, g := range glyphs {
		if len(rows) > 0 && abs(g.top-refTop) <= 0.5*min(g.size, refSize) {
			rows[len(rows)-1] = append(rows[len(rows)-1], g)
			continue
		}
		rows = append(rows, []glyph{g})
		refTop, refSize = g.top, g.size
	}

	for _, row := range rows {
		sort.Slice(row, func(i, j int) bool { return row[i].x < row[j].x })
		for _, run := range splitRow(row) {
			if ln, ok := buildLine(run); ok {
				ann.Lines = append(ann.Lines, ln)
			}
		}
	}
	return ann
}

// splitRow cuts one baseline row at column-sized horizontal gaps
// (>= 2.2× the glyph size): the two columns of a two-column page share
// a baseline but never a line.
func splitRow(row []glyph) [][]glyph {
	var out [][]glyph
	start := 0
	for i := 1; i < len(row); i++ {
		if row[i].x-row[i-1].right >= 2.2*max(row[i].size, row[i-1].size) {
			out = append(out, row[start:i])
			start = i
		}
	}
	return append(out, row[start:])
}

// buildLine turns one horizontally contiguous glyph run into an
// annotation line. Words split at word-space gaps (>= 0.2× the glyph
// size — a Helvetica space is 0.278 em, intra-word glyph gaps are ~0).
// The line box height is the largest glyph size on the line: exactly
// the signal the tree builder's heading detector compares against the
// area-weighted body height.
func buildLine(run []glyph) (pb.Line, bool) {
	if len(run) == 0 {
		return pb.Line{}, false
	}
	size, top := 0.0, run[0].top
	left, right := run[0].x, run[0].right
	for _, g := range run {
		size = max(size, g.size)
		top = min(top, g.top)
		left, right = min(left, g.x), max(right, g.right)
	}

	ln := pb.Line{
		Words:      []pb.Word{},
		Box:        rect(left, top, right-left, size),
		Confidence: 1, // the text layer is exact — no recognition error
	}
	var words []string
	var w *pb.Word
	var wStart, wRight float64
	closeWord := func() {
		if w == nil {
			return
		}
		w.Box = rect(wStart, top, wRight-wStart, size)
		ln.Words = append(ln.Words, *w)
		words = append(words, w.Text)
		w = nil
	}
	for i, g := range run {
		if w != nil && g.x-run[i-1].right < 0.2*min(g.size, run[i-1].size) {
			w.Text += g.s
			wRight = max(wRight, g.right)
			continue
		}
		closeWord()
		w = &pb.Word{Text: g.s, HasSpaceAfter: true}
		wStart, wRight = g.x, g.right
	}
	closeWord()
	if len(ln.Words) > 0 {
		ln.Words[len(ln.Words)-1].HasSpaceAfter = false
	}
	ln.Text = strings.Join(words, " ")
	if strings.TrimSpace(ln.Text) == "" {
		return pb.Line{}, false
	}
	return ln, true
}

// rect rounds a float box into the integer pixel box the annotation
// carries, never collapsing below 1×1.
func rect(x, y, w, h float64) *pb.Rect {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	return &pb.Rect{X: int(x + 0.5), Y: int(y + 0.5), Width: int(w + 0.5), Height: int(h + 0.5)}
}

// repairCollapsed restores glyph geometry when the PDF omits /Widths
// for a standard-14 font — legal per the spec (viewers fall back to
// built-in metrics the parser does not carry), and common in output of
// minimal generators. Without widths every glyph of a text object
// collapses onto the text origin; estimated advances (space 0.278em,
// narrow 0.5em, full-width 1em) lay the run back on the line. Glyphs
// with working widths are never touched.
func repairCollapsed(gs []glyph) {
	for i := 0; i < len(gs); {
		if gs[i].w > 0 {
			i++
			continue
		}
		// A collapsed run is one text object: same x origin AND same
		// baseline (the top). Consecutive text objects usually jump the
		// baseline — without this, a second line at the same x would be
		// estimated as a continuation of the first and drift right.
		j := i
		for j+1 < len(gs) && gs[j+1].w == 0 && gs[j+1].x == gs[i].x && gs[j+1].top == gs[i].top {
			j++
		}
		pos := gs[i].x
		for k := i; k <= j; k++ {
			gs[k].x = pos
			gs[k].w = estimatedAdvance(gs[k].s, gs[k].size)
			gs[k].right = pos + gs[k].w
			pos = gs[k].right
		}
		i = j + 1
	}
}

func estimatedAdvance(s string, size float64) float64 {
	r := []rune(s)
	if len(r) == 0 {
		return 0
	}
	switch {
	case unicode.IsSpace(r[0]):
		return 0.278 * size // a Helvetica space is 0.278 em
	case isFullWidth(r[0]):
		return size // CJK ideographs occupy a full em
	default:
		return 0.5 * size // average Latin glyph advance
	}
}

func isFullWidth(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) ||
		(r >= 0x3000 && r <= 0x303F) || (r >= 0xFF00 && r <= 0xFF60)
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
