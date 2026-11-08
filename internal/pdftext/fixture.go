package pdftext

import (
	"bytes"
	"fmt"
	"strings"
)

// FixtureLine is one text line of a Fixture page: Text drawn with
// Helvetica at Size points, baseline at (X, Y) in PDF user space
// (origin bottom-left, Y growing upwards).
type FixtureLine struct {
	Text string
	Size float64
	X, Y float64
}

// Fixture builds a small but fully valid PDF: one shared Type1
// Helvetica font, one content stream per page, a byte-exact
// cross-reference table. It exists so the PDF text-layer path can be
// tested end to end — including by `screenai selftest` — without
// shipping binary fixture files.
//
// The font deliberately carries no /Widths: relying on the standard-14
// built-in metrics is legal per the PDF spec and common in minimal
// generators, so the parser must survive it (the repair pass handles
// it). FixtureEmbedded is the variant with real widths.
func Fixture(pages [][]FixtureLine) []byte {
	return fixture(pages, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
}

// FixtureEmbedded is Fixture with a font that carries true Helvetica
// /Widths for characters 32..126, like an embedded-font PDF: glyph
// geometry then comes from the declared advances, not estimates.
func FixtureEmbedded(pages [][]FixtureLine) []byte {
	return fixture(pages, fmt.Sprintf("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /FirstChar 32 /LastChar 126 /Widths [%s] >>",
		strings.Join(helveticaWidths(), " ")))
}

func fixture(pages [][]FixtureLine, fontObj string) []byte {
	var buf bytes.Buffer
	var offsets []int // offsets[i] = byte offset of object i+1

	writeObj := func(body string) {
		offsets = append(offsets, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", len(offsets), body)
	}

	// The header must be exactly "%PDF-1.x" followed by a newline —
	// the parser checks that byte-for-byte.
	buf.WriteString("%PDF-1.4\n")

	n := len(pages)
	kids := make([]string, n)
	for i := range kids {
		kids[i] = fmt.Sprintf("%d 0 R", 4+2*i) // page objects start at 4
	}
	writeObj("<< /Type /Catalog /Pages 2 0 R >>")
	writeObj(fmt.Sprintf("<< /Type /Pages /Kids [ %s ] /Count %d >>", strings.Join(kids, " "), n))
	writeObj(fontObj)
	for i, page := range pages {
		var content bytes.Buffer
		for _, ln := range page {
			fmt.Fprintf(&content, "BT /F1 %g Tf %g %g Td (%s) Tj ET\n", ln.Size, ln.X, ln.Y, escapePDFString(ln.Text))
		}
		writeObj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", 5+2*i))
		writeObj(fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", content.Len(), content.String()))
	}

	xref := buf.Len()
	buf.WriteString(fmt.Sprintf("xref\n0 %d\n", len(offsets)+1))
	buf.WriteString("0000000000 65535 f \n")
	for _, off := range offsets {
		buf.WriteString(fmt.Sprintf("%010d 00000 n \n", off))
	}
	buf.WriteString(fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref))
	return buf.Bytes()
}

// SimpleFixture is a two-page PDF with a 24pt heading over 11pt body
// text on page one, and an 18pt heading over one body line on page two
// — the minimal shape the heading detector needs to see.
func SimpleFixture() []byte {
	return Fixture([][]FixtureLine{
		{
			{Text: "Fixture Heading", Size: 24, X: 72, Y: 700},
			{Text: "Body line one on the first page.", Size: 11, X: 72, Y: 660},
			{Text: "Body line two on the first page.", Size: 11, X: 72, Y: 640},
			{Text: "Body line three on the first page.", Size: 11, X: 72, Y: 620},
			{Text: "Body line four on the first page.", Size: 11, X: 72, Y: 600},
			{Text: "Body line five on the first page.", Size: 11, X: 72, Y: 580},
			{Text: "Body line six on the first page.", Size: 11, X: 72, Y: 560},
		},
		{
			{Text: "Second page heading", Size: 18, X: 72, Y: 700},
			{Text: "Only body line on the second page.", Size: 11, X: 72, Y: 660},
			{Text: "Another body line on the second page.", Size: 11, X: 72, Y: 640},
			{Text: "A third body line on the second page.", Size: 11, X: 72, Y: 620},
		},
	})
}

func escapePDFString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`)
	return r.Replace(s)
}

// helveticaWidths returns the standard Helvetica glyph advance widths
// (in 1/1000 em, the unit PDF /Widths uses) for characters 32..126 —
// the AFM metrics every PDF viewer carries for the standard-14 fonts.
func helveticaWidths() []string {
	widths := []int{
		278, 278, 355, 556, 556, 889, 667, 191, 333, 333, //   .. /
		389, 584, 278, 333, 278, 278, // :  .. ?
		556, 556, 556, 556, 556, 556, 556, 556, 556, 556, // 0  .. 9
		278, 278, 584, 584, 584, 556, // :  .. ?
		1015,                                             // @
		667, 667, 722, 722, 667, 611, 778, 722, 278, 500, // A  .. J
		667, 556, 833, 722, 778, 667, 778, 722, 667, 611, // K  .. T
		722, 667, 944, 667, 667, 611, // U  .. Z
		278, 278, 278, 469, 556, 333, // [  .. `
		556, 556, 500, 556, 556, 278, 556, 556, 222, 222, // a  .. j
		500, 222, 833, 556, 556, 556, // k  .. p
		556, 556, 500, 556, 278, 556, // q  .. v
		722, 500, 500, 500, // w  .. z
		334, 260, 334, 584, // {  .. ~
	}
	out := make([]string, len(widths))
	for i, w := range widths {
		out[i] = fmt.Sprintf("%d", w)
	}
	return out
}
