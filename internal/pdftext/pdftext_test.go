package pdftext

import (
	"strings"
	"testing"
)

func lineTexts(p Page) []string {
	out := make([]string, 0, len(p.Ann.Lines))
	for _, ln := range p.Ann.Lines {
		out = append(out, ln.Text)
	}
	return out
}

func TestParseSimpleFixture(t *testing.T) {
	doc, err := Parse(SimpleFixture(), "fixture.pdf")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.Pages) != 2 {
		t.Fatalf("got %d pages, want 2", len(doc.Pages))
	}
	p := doc.Pages[0]
	if p.Width != 612 || p.Height != 792 {
		t.Fatalf("page 1 dims %dx%d, want 612x792", p.Width, p.Height)
	}
	if p.Number != 1 || doc.Pages[1].Number != 2 {
		t.Fatalf("page numbers %d, %d — want 1, 2", p.Number, doc.Pages[1].Number)
	}
	if p.Chunks == 0 {
		t.Fatal("no text-layer glyphs parsed")
	}
	want := []string{
		"Fixture Heading",
		"Body line one on the first page.",
		"Body line two on the first page.",
		"Body line three on the first page.",
		"Body line four on the first page.",
		"Body line five on the first page.",
		"Body line six on the first page.",
	}
	got := lineTexts(p)
	if len(got) != len(want) {
		t.Fatalf("page 1 lines = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}

	// Exact geometry: baseline Y=700, size 24, page height 792 →
	// top = 792-700-24 = 68; body baseline 660, size 11 → top = 121.
	h := p.Ann.Lines[0]
	if h.Box.X != 72 || h.Box.Y != 68 || h.Box.Height != 24 {
		t.Errorf("heading box = %+v, want X=72 Y=68 H=24", h.Box)
	}
	if h.Confidence != 1 {
		t.Errorf("confidence = %g, want 1 (text layer is exact)", h.Confidence)
	}
	b := p.Ann.Lines[1]
	if b.Box.X != 72 || b.Box.Y != 121 || b.Box.Height != 11 {
		t.Errorf("body box = %+v, want X=72 Y=121 H=11 (lines must not drift right)", b.Box)
	}
	if last := p.Ann.Lines[6]; last.Box.X != 72 {
		t.Errorf("last body box X = %d, want 72 (per-line runs restart at the text origin)", last.Box.X)
	}
	// Reading order: heading (top=68) before body lines (121, 141).
	if !(h.Box.Y < b.Box.Y) {
		t.Errorf("lines not in top-to-bottom order: heading Y=%d, body Y=%d", h.Box.Y, b.Box.Y)
	}

	// Words carry boxes inside the line box, text joins back.
	if len(h.Words) != 2 || h.Words[0].Text != "Fixture" || h.Words[1].Text != "Heading" {
		texts := make([]string, len(h.Words))
		for i, w := range h.Words {
			texts[i] = w.Text
		}
		t.Fatalf("heading words = %q, want [Fixture Heading]", texts)
	}
	for _, w := range h.Words {
		if w.Box == nil || w.Box.Y < h.Box.Y || w.Box.Y+w.Box.Height > h.Box.Y+h.Box.Height+1 {
			t.Errorf("word box %+v escapes line box %+v", w.Box, h.Box)
		}
	}
}

func TestParsePageTwoAndSource(t *testing.T) {
	doc, err := Parse(SimpleFixture(), "doc.pdf")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	p2 := doc.Pages[1]
	want := []string{
		"Second page heading",
		"Only body line on the second page.",
		"Another body line on the second page.",
		"A third body line on the second page.",
	}
	got := lineTexts(p2)
	if len(got) != len(want) {
		t.Fatalf("page 2 lines = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
	if !strings.Contains(p2.Ann.Source, "p2/2") {
		t.Errorf("source = %q, want it to carry the page number", p2.Ann.Source)
	}
	if p2.Ann.Width != 612 || p2.Ann.Height != 792 {
		t.Errorf("annotation dims = %dx%d, want 612x792", p2.Ann.Width, p2.Ann.Height)
	}
}

func TestColumnSplitting(t *testing.T) {
	// Same baseline, two columns 2.5 body sizes apart: one row, two lines.
	doc, err := Parse(Fixture([][]FixtureLine{{
		{Text: "Left column.", Size: 11, X: 72, Y: 700},
		{Text: "Right column.", Size: 11, X: 330, Y: 700},
	}}), "cols.pdf")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := lineTexts(doc.Pages[0])
	if len(got) != 2 {
		t.Fatalf("lines = %q, want 2 (column gap must split the row)", got)
	}
	if got[0] != "Left column." || got[1] != "Right column." {
		t.Fatalf("lines = %q, want [Left column. Right column.]", got)
	}
}

func TestWordSplittingWithinLine(t *testing.T) {
	// One Tj draws "alpha beta gamma": the space glyphs advance the
	// origin, so the gap threshold must split it into 3 words.
	doc, err := Parse(Fixture([][]FixtureLine{{
		{Text: "alpha beta gamma", Size: 12, X: 72, Y: 700},
	}}), "words.pdf")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ln := doc.Pages[0].Ann.Lines[0]
	if ln.Text != "alpha beta gamma" {
		t.Fatalf("line text = %q", ln.Text)
	}
	if len(ln.Words) != 3 {
		texts := make([]string, len(ln.Words))
		for i, w := range ln.Words {
			texts[i] = w.Text
		}
		t.Fatalf("words = %q, want [alpha beta gamma]", texts)
	}
}

func TestNoTextPage(t *testing.T) {
	doc, err := Parse(Fixture([][]FixtureLine{
		{{Text: "Page one text.", Size: 11, X: 72, Y: 700}},
		{}, // scanned page: content stream with no text
	}), "mixed.pdf")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Pages[1].Chunks != 0 {
		t.Fatalf("page 2 chunks = %d, want 0", doc.Pages[1].Chunks)
	}
	if len(doc.Pages[1].Ann.Lines) != 0 {
		t.Fatalf("page 2 lines = %d, want 0", len(doc.Pages[1].Ann.Lines))
	}
}

func TestParseGarbage(t *testing.T) {
	for _, bad := range [][]byte{
		nil,
		[]byte("not a pdf at all"),
		[]byte("%PDF-1.4\nbroken"),
	} {
		if _, err := Parse(bad, "bad.pdf"); err == nil {
			t.Fatalf("Parse(%q) succeeded, want error", bad)
		}
	}
}

func TestEmbeddedFontWidths(t *testing.T) {
	// With real /Widths the geometry comes from the declared advances:
	// "Hi" at 24pt from x=72 → H advances 722/1000×24 = 17.328, i
	// advances 222/1000×24 = 5.328 → line width ≈ 22.66 → 23.
	doc, err := Parse(FixtureEmbedded([][]FixtureLine{{
		{Text: "Hi", Size: 24, X: 72, Y: 700},
	}}), "embed.pdf")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ln := doc.Pages[0].Ann.Lines[0]
	if ln.Text != "Hi" {
		t.Fatalf("text = %q, want Hi", ln.Text)
	}
	if ln.Box.X != 72 || ln.Box.Width != 23 {
		t.Errorf("box = %+v, want X=72 W=23", ln.Box)
	}
	// The real space advance (278/1000×12 = 3.3pt ≥ 0.2×12) must split
	// words on the embedded path too.
	doc2, err := Parse(FixtureEmbedded([][]FixtureLine{{
		{Text: "alpha beta gamma", Size: 12, X: 72, Y: 700},
	}}), "embed2.pdf")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ln2 := doc2.Pages[0].Ann.Lines[0]
	if len(ln2.Words) != 3 {
		t.Fatalf("words = %d, want 3", len(ln2.Words))
	}
}

func TestFixtureHeaderAndEOF(t *testing.T) {
	b := SimpleFixture()
	if !strings.HasPrefix(string(b), "%PDF-1.4\n") {
		t.Error("fixture header malformed")
	}
	if !strings.HasSuffix(string(b), "%%EOF\n") {
		t.Error("fixture EOF malformed")
	}
}
