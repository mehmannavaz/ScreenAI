package pipeline

import (
	"strings"
	"testing"

	"github.com/mehmannavaz/ScreenAI/internal/pagetree"
	"github.com/mehmannavaz/ScreenAI/internal/pdftext"
)

func TestClassifyPDF(t *testing.T) {
	inp, err := Classify(pdftext.SimpleFixture(), "doc.pdf", "", "doc.pdf")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if inp.Kind != KindPDF || inp.Format != "PDF" {
		t.Fatalf("kind=%s format=%s, want pdf/PDF", inp.Kind, inp.Format)
	}
	// Sniffing wins even without the extension (URL downloads).
	inp2, err := Classify(pdftext.SimpleFixture(), "download", "", "download")
	if err != nil || inp2.Kind != KindPDF {
		t.Fatalf("sniffed kind=%s err=%v, want pdf", inp2.Kind, err)
	}
	// A .pdf extension over non-PDF bytes must fail clearly.
	if _, err := Classify([]byte("plain text, not a pdf"), "doc.pdf", "", "doc.pdf"); err == nil {
		t.Fatal("Classify accepted a non-PDF with a .pdf extension")
	}
}

func TestExpandPDF(t *testing.T) {
	inp, _ := Classify(pdftext.SimpleFixture(), "doc.pdf", "", "doc.pdf")
	exp, err := ExpandPDF(inp)
	if err != nil {
		t.Fatalf("ExpandPDF: %v", err)
	}
	if exp.Total != 2 || len(exp.Pages) != 2 || len(exp.NoText) != 0 {
		t.Fatalf("total=%d pages=%d noText=%d, want 2/2/0", exp.Total, len(exp.Pages), len(exp.NoText))
	}
	p1 := exp.Pages[0]
	if p1.Name != "doc.p1.pdf" || p1.Kind != KindPDFPage || p1.Format != "PDF" {
		t.Fatalf("page input name=%s kind=%s format=%s", p1.Name, p1.Kind, p1.Format)
	}
	if p1.Page != 1 || p1.PageCount != 2 || p1.Width != 612 || p1.Height != 792 {
		t.Fatalf("page geometry: %+v", p1)
	}
	if p1.Ann == nil || len(p1.Ann.Lines) != 7 {
		t.Fatalf("page 1 annotation lines=%d, want 7 (1 heading + 6 body)", len(p1.Ann.Lines))
	}

	// The exact chain Process runs for a pdfpage input: the semantic
	// tree from the synthesized annotation must carry a heading node
	// (24pt title over 11pt body, default ratio 1.3).
	tree := pagetree.FromOCR(p1.Ann, nil, p1.Width, p1.Height, pagetree.DefaultTreeOptions())
	found := false
	for _, n := range tree.Nodes {
		if n.Node.Role == "heading" && strings.Contains(pagetree.SubtreeText(n.Node), "Fixture Heading") {
			found = true
			if n.Node.Level == nil || *n.Node.Level != 1 {
				t.Errorf("heading level = %v, want 1 (24/11 ≈ 2.2× body)", n.Node.Level)
			}
		}
	}
	if !found {
		t.Fatal("no heading node in the page-1 tree")
	}
}

func TestExpandPDFScannedOnly(t *testing.T) {
	// A PDF whose pages carry no text layer at all: the honest error
	// points at the rasterize-to-PNG escape hatch.
	scanned := pdftext.Fixture([][]pdftext.FixtureLine{{}, {}})
	inp, _ := Classify(scanned, "scan.pdf", "", "scan.pdf")
	exp, err := ExpandPDF(inp)
	if err == nil {
		t.Fatalf("ExpandPDF succeeded with %d pages, want error", len(exp.Pages))
	}
	if !strings.Contains(err.Error(), "text layer") || !strings.Contains(err.Error(), "PNG") {
		t.Fatalf("error %q does not explain the scanned-PDF situation", err)
	}
}

func TestExpandPDFMixedPages(t *testing.T) {
	// Page two is a scan: skipped, not failed; page one still processes.
	mixed := pdftext.Fixture([][]pdftext.FixtureLine{
		{{Text: "Page one text.", Size: 11, X: 72, Y: 700}},
		{},
	})
	inp, _ := Classify(mixed, "mixed.pdf", "", "mixed.pdf")
	exp, err := ExpandPDF(inp)
	if err != nil {
		t.Fatalf("ExpandPDF: %v", err)
	}
	if len(exp.Pages) != 1 || exp.Pages[0].Page != 1 {
		t.Fatalf("pages=%d, want the single text page", len(exp.Pages))
	}
	if len(exp.NoText) != 1 || exp.NoText[0] != 2 {
		t.Fatalf("noText=%v, want [2]", exp.NoText)
	}
}

func TestExpandPDFFailsOnWrongKind(t *testing.T) {
	inp := &Input{Name: "x.png", Kind: KindImage}
	if _, err := ExpandPDF(inp); err == nil {
		t.Fatal("ExpandPDF accepted a non-pdf input")
	}
}
