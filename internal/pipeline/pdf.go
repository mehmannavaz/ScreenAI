package pipeline

import (
	"fmt"
	"path"
	"strings"

	"github.com/mehmannavaz/ScreenAI/internal/pdftext"
)

// PDFExpansion is the result of expanding one PDF document into
// processable page inputs.
type PDFExpansion struct {
	// Pages holds one input per page that carries a text layer, in
	// page order. Each processes through the same Screen2x pipeline as
	// an image: the synthesized annotation drives the semantic tree.
	Pages []*Input
	// NoText lists the 1-based numbers of pages without a text layer
	// (scanned pages) — they are skipped, not failed.
	NoText []int
	// Total is the document's page count.
	Total int
}

// ExpandPDF parses the text layer of a KindPDF input and turns every
// page into a KindPDFPage input carrying its synthesized annotation.
//
// Page inputs are named "{stem}.p{N}.pdf" so the result-file stems come
// out as "{stem}.p{N}" — report.ocr.json, report.mce.json, one HTML
// report per page — while the whole document additionally gets a merged
// reader view under "{stem}.reader.{ext}".
//
// A document whose pages carry no text layer at all (a pure scan)
// cannot go through this path: it is reported as an unsupported input
// with the rasterize-to-PNG instruction instead of silently producing
// empty results.
func ExpandPDF(inp *Input) (*PDFExpansion, error) {
	if inp.Kind != KindPDF {
		return nil, fmt.Errorf("pipeline: ExpandPDF on a %s input", inp.Kind)
	}
	doc, err := pdftext.Parse(inp.Data, inp.Name)
	if err != nil {
		return nil, err
	}
	base := path.Base(inp.Name)
	if ext := path.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	if base == "" {
		base = "document"
	}
	exp := &PDFExpansion{Total: len(doc.Pages)}
	for i := range doc.Pages {
		p := &doc.Pages[i]
		if p.Chunks == 0 {
			exp.NoText = append(exp.NoText, p.Number)
			continue
		}
		exp.Pages = append(exp.Pages, &Input{
			Name:      fmt.Sprintf("%s.p%d.pdf", base, p.Number),
			Hint:      inp.Hint,
			Kind:      KindPDFPage,
			Format:    "PDF",
			Ann:       p.Ann,
			Page:      p.Number,
			PageCount: len(doc.Pages),
			Width:     p.Width,
			Height:    p.Height,
		})
	}
	if len(exp.Pages) == 0 {
		return nil, fmt.Errorf("%w: %s: no page carries a text layer (scanned PDF?) — rasterize the pages to PNG and run them as images", ErrUnsupportedInput, inp.Name)
	}
	return exp, nil
}
