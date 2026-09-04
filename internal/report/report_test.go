package report

import (
	"strings"
	"testing"

	"github.com/mehmannavaz/ScreenAI/internal/pb"
	"github.com/mehmannavaz/ScreenAI/internal/pipeline"
)

// testReportData builds a minimal but realistic ReportData: a 1000x2000
// image with two OCR lines and a three-node MCE result where node 105
// is not selected and node 107 is selected.
func testReportData(ocrOn, mceOn bool) *pipeline.ReportData {
	box := func(x, y, w, h int) *pb.Rect { return &pb.Rect{X: x, Y: y, Width: w, Height: h} }
	crop := [4]int{100, 200, 600, 800}
	return &pipeline.ReportData{
		OCRBoxesOn: ocrOn,
		MCEBoxesOn: mceOn,
		ImageW:     1000,
		ImageH:     2000,
		PNGURI:     "data:image/png;base64,AAAA",
		OCR: &pb.Annotation{
			Width: 1000, Height: 2000,
			Lines: []pb.Line{
				{Text: "Hello world", Language: "en", Confidence: 0.99, Box: box(100, 200, 500, 40)},
				{Text: "Second line", Language: "en", Confidence: 0.55, Box: box(100, 300, 400, 40)},
			},
		},
		MCE: &pipeline.MCE{
			Ran:              true,
			ContentAXNodeIDs: []int{107},
			NodeCount:        3,
			Nodes: []pipeline.MCENode{
				{AXNodeID: 105, Role: "paragraph", SubtreeText: "not main", Box: [4]int{10, 10, 90, 90}},
				{AXNodeID: 106, Role: "banner", Box: [4]int{0, 0, 0, 0}}, // degenerate box
				{AXNodeID: 107, Role: "staticText", Text: "main text", Box: [4]int{100, 200, 600, 800}},
			},
			CropBox: &crop,
		},
	}
}

func TestReportOverlayRendering(t *testing.T) {
	html := Build("test", MetaView{Kind: "image", Format: "png", Version: "v", Elapsed: "0s"},
		testReportData(true, true), &pipeline.Result{OK: true, Format: "png"})

	// The one failure mode that made every box invisible before: the
	// style attribute must never contain the html/template sentinel.
	if strings.Contains(html, "ZgotmplZ") {
		t.Fatal("overlay style attribute was replaced by ZgotmplZ — template.CSS plumbing broken")
	}

	// OCR layer: two boxes with real positioning and confidence colors.
	if got := strings.Count(html, `class="obox"`); got != 2 {
		t.Fatalf("OCR overlay boxes: got %d, want 2", got)
	}
	if !strings.Contains(html, `id="lay-ocr"`) {
		t.Fatal("OCR layer container missing")
	}
	if !strings.Contains(html, "left:10.000%;top:10.000%;width:50.000%;height:2.000%") {
		t.Fatal("OCR box style not positioned in percentages of the image")
	}
	if !strings.Contains(html, "border-color:#34a853") || !strings.Contains(html, "border-color:#f28b82") {
		t.Fatal("confidence border colors missing")
	}

	// MCE layer: exactly the selected node (degenerate boxes skipped).
	if got := strings.Count(html, `class="mbox"`); got != 1 {
		t.Fatalf("MCE overlay boxes: got %d, want 1 (selected node only)", got)
	}
	if !strings.Contains(html, `id="lay-mce"`) {
		t.Fatal("MCE layer container missing")
	}
	if !strings.Contains(html, "main text") || !strings.Contains(html, "staticText") {
		t.Fatal("MCE box title (role/text) missing")
	}
	if !strings.Contains(html, `class="cropbox"`) {
		t.Fatal("dashed main-content region box missing")
	}

	// Toggles + image.
	if !strings.Contains(html, `class="lay-toggle ocr"`) || !strings.Contains(html, `class="lay-toggle mce"`) {
		t.Fatal("layer toggle pills missing")
	}
	if !strings.Contains(html, `src="data:image/png;base64,AAAA"`) {
		t.Fatal("embedded image missing")
	}
	if !strings.Contains(html, "What the models detected") {
		t.Fatal("stage card caption missing")
	}
}

func TestReportOverlayDisabled(t *testing.T) {
	// report.ocr_boxes=false / report.mce_boxes=false: layers are not
	// embedded in the file at all, but the image itself still is.
	html := Build("test", MetaView{Kind: "image", Format: "png", Version: "v", Elapsed: "0s"},
		testReportData(false, false), &pipeline.Result{OK: true, Format: "png"})

	if strings.Contains(html, `class="obox"`) || strings.Contains(html, `id="lay-ocr"`) {
		t.Fatal("OCR layer embedded despite report.ocr_boxes=false")
	}
	if strings.Contains(html, `class="mbox"`) || strings.Contains(html, `id="lay-mce"`) || strings.Contains(html, `class="cropbox"`) {
		t.Fatal("MCE layer embedded despite report.mce_boxes=false")
	}
	if !strings.Contains(html, `src="data:image/png;base64,AAAA"`) {
		t.Fatal("image itself must stay embedded when overlays are off")
	}
	if !strings.Contains(html, "Input image") {
		t.Fatal("caption should fall back to 'Input image' with no layers")
	}
}

func TestReportOverlayPartial(t *testing.T) {
	// Only the MCE layer disabled: OCR layer stays.
	html := Build("test", MetaView{Kind: "image", Format: "png", Version: "v", Elapsed: "0s"},
		testReportData(true, false), &pipeline.Result{OK: true, Format: "png"})
	if got := strings.Count(html, `class="obox"`); got != 2 {
		t.Fatalf("OCR boxes: got %d, want 2", got)
	}
	if strings.Contains(html, "class=\"mbox\"") {
		t.Fatal("MCE layer embedded despite report.mce_boxes=false")
	}
	if strings.Contains(html, `class="lay-toggle mce"`) {
		t.Fatal("MCE toggle pill should be absent")
	}
	if !strings.Contains(html, `class="lay-toggle ocr"`) {
		t.Fatal("OCR toggle pill should be present")
	}
}

func TestReportNoImage(t *testing.T) {
	// Tree inputs (HTML/JSON/TXT) have no PNG: no stage, no dead script.
	data := testReportData(true, true)
	data.PNGURI = ""
	data.ImageW, data.ImageH = 0, 0
	html := Build("test", MetaView{Kind: "text", Format: "txt", Version: "v", Elapsed: "0s"},
		data, &pipeline.Result{OK: true, Format: "txt"})
	if strings.Contains(html, `<div class="imgstage">`) || strings.Contains(html, "<script>") {
		t.Fatal("image stage or toggle script rendered for a non-image input")
	}
}
