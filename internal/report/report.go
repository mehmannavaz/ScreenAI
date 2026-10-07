// Package report renders the self-contained HTML reports: one report
// page per processed input (image with OCR overlay, recognized text,
// MCE reader view, node table) plus an index hub linking them all.
package report

import (
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mehmannavaz/ScreenAI/internal/pipeline"
)

// Entry is one index entry: the outcome of one processed input.
type Entry struct {
	Stem    string
	Format  string
	Kind    string
	Source  string
	Elapsed float64
	HasOCR  bool
	HasMCE  bool
	Summary string
	HTML    string
}

// Stem sanitizes a file name into a safe result-file stem.
func Stem(name string) string {
	base := filepath.Base(name)
	if ext := filepath.Ext(base); ext != "" {
		base = base[:len(base)-len(ext)]
	}
	if base == "" {
		base = name
	}
	re := regexp.MustCompile(`[^A-Za-z0-9._-]+`)
	return re.ReplaceAllString(base, "_")
}

// BoxView is one overlay box positioned in percentages of the image.
// Style carries the complete CSS declaration list (position and border
// color) as a template.CSS value: html/template passes trusted CSS
// through unchanged inside a style attribute, while plain strings and
// template.HTMLAttr values are replaced with "ZgotmplZ" — which is
// exactly how every overlay box used to silently vanish.
type BoxView struct {
	Style template.CSS
	Title string
}

// StageView is the image stage: the embedded preview of the image the
// models actually saw, with the OCR and main-content overlay layers
// drawn on top. Each layer can be toggled in-page; whether a layer is
// embedded at all is controlled by report.ocr_boxes / report.mce_boxes.
type StageView struct {
	HasImage bool
	ImageURI template.URL
	ImageW   int
	ImageH   int

	HasOCR   bool // OCR layer embedded (report.ocr_boxes)
	OCRCount int
	OCRBoxes []BoxView

	HasMCE   bool // main-content layer embedded (report.mce_boxes)
	MCECount int
	MCEBoxes []BoxView

	HasCrop   bool         // dashed main-content region (union box)
	CropStyle template.CSS // full style for the crop box

	HasLayers bool // any overlay layer present (card caption)
}

// LineView is one row of the OCR lines table.
type LineView struct {
	N     int
	Box   string
	Conf  string
	Color string
	Lang  string
	Block int
	Text  string
}

// StatsView is one stat chip pair.
type StatsView struct {
	Label string
	Value template.HTML
}

// OCRView is the OCR section view model. (The image and its overlay
// layers live in StageView.)
type OCRView struct {
	PlainText string
	Lines     []LineView
	LineCount int
	WordCount int
	AvgConf   string
	Languages string
}

// ReaderView is one reader-view block.
type ReaderView struct {
	IsHeading bool
	IsItem    bool
	Text      string
}

// OutlineView is one outline (in-content heading) row.
type OutlineView struct {
	Level int
	Text  string
}

// NodeView is one row of the MCE node table.
type NodeView struct {
	IsMain   bool
	Type     string // headline | main | nonessential
	Order    int
	AXNode   int
	Role     string
	RoleNote string // "L2" for level-2 headings, "" otherwise
	Box      string
	Font     string
	Text     string
}

// MCEView is the MCE section view model.
type MCEView struct {
	Ran         bool
	HasSelected bool
	MainRole    string
	MainText    string
	Selected    int
	Total       int
	MainProto   string
	Reader      []ReaderView
	ReaderMeta  string
	Outline     []OutlineView
	ReaderWords int
	ReadingSec  int
	ClutterPct  string
	CropBox     string
	Headlines   int
	Roles       string // selected-by-role summary, e.g. "2×heading · 5×paragraph"
	Nodes       []NodeView
}

// MetaView is the report header.
type MetaView struct {
	Kind    string
	Format  string
	Version string
	Elapsed string
	Source  string
}

// ReportView is the full report page view model.
type ReportView struct {
	Title    string
	Subtitle string
	Back     bool
	Stem     string
	Stamps   string
	HasOCR   bool
	HasMCE   bool
	Stage    StageView
	OCR      OCRView
	MCE      MCEView
	Meta     MetaView
	Chips    []template.HTML
	RawJSON  string
}

// Build renders the one-file HTML report for a single processed input.
func Build(stem string, meta MetaView, ocr *pipeline.ReportData, res *pipeline.Result) string {
	view := ReportView{
		Title:  stem + " — Screen AI report",
		Stem:   stem,
		Back:   true,
		Stamps: time.Now().Format("2006-01-02 15:04"),
		Meta:   meta,
	}
	view.Subtitle = fmt.Sprintf("%s input · %s · library %s · %s · %s",
		meta.Format, meta.Kind, meta.Version, meta.Elapsed, view.Stamps)

	if ocr != nil {
		buildStageView(&view.Stage, ocr)
	}
	if ocr != nil && ocr.OCR != nil {
		view.HasOCR = true
		buildOCRView(&view.OCR, ocr)
		view.Chips = append(view.Chips, template.HTML(fmt.Sprintf(
			"OCR: <b>%d</b> lines, <b>%d</b> words", len(ocr.OCR.Lines), ocr.OCR.WordCount())))
	}
	if ocr != nil && ocr.MCE != nil && ocr.MCE.Ran {
		view.HasMCE = true
		buildMCEView(&view.MCE, ocr.MCE)
		view.Chips = append(view.Chips, template.HTML(fmt.Sprintf(
			"MCE: <b>%d</b> main-content nodes", len(ocr.MCE.ContentAXNodeIDs))))
	}

	if view.HasOCR || view.HasMCE {
		dump := map[string]any{"meta": meta}
		if view.HasOCR {
			dump["ocr"] = ocr.OCR
		}
		if view.HasMCE {
			dump["main_content"] = ocr.MCE
		}
		if b, err := json.MarshalIndent(dump, "", " "); err == nil {
			s := string(b)
			if len(s) > 200000 {
				s = s[:200000]
			}
			view.RawJSON = s
		}
	}
	return renderPage(reportTemplate, view)
}

// IndexView is the index hub view model.
type IndexView struct {
	Title   string
	Back    bool
	Stamps  string
	Entries []Entry
}

// BuildIndex renders the index.html hub linking all reports of one run.
func BuildIndex(entries []Entry) string {
	view := IndexView{
		Title:   "Screen AI results",
		Stamps:  time.Now().Format("2006-01-02 15:04"),
		Entries: entries,
	}
	return renderPage(indexTemplate, view)
}

func buildOCRView(view *OCRView, data *pipeline.ReportData) {
	ann := data.OCR
	view.LineCount = len(ann.Lines)
	view.WordCount = ann.WordCount()
	view.AvgConf = fmt.Sprintf("%.3f", ann.AvgConfidence())
	view.Languages = strings.Join(ann.Languages(), ", ")
	view.PlainText = ann.PlainText()

	for i := range ann.Lines {
		ln := &ann.Lines[i]
		box := ""
		if ln.Box != nil {
			box = fmt.Sprintf("(%d,%d %dx%d)", ln.Box.X, ln.Box.Y, ln.Box.Width, ln.Box.Height)
		}
		view.Lines = append(view.Lines, LineView{
			N: i + 1, Box: box,
			Conf:  fmt.Sprintf("%.3f", ln.Confidence),
			Color: confColor(float64(ln.Confidence)),
			Lang:  ln.Language, Block: ln.BlockID, Text: ln.Text,
		})
	}
}

func buildMCEView(view *MCEView, mce *pipeline.MCE) {
	view.Ran = true
	view.Selected = len(mce.ContentAXNodeIDs)
	view.Total = mce.NodeCount
	view.HasSelected = view.Selected > 0
	view.MainRole = mce.MainRole
	view.MainText = mce.MainText
	if mce.MainNodeProtoID != nil {
		view.MainProto = fmt.Sprintf("%d", *mce.MainNodeProtoID)
	}
	view.ReaderMeta = mce.MainText
	for _, o := range mce.Outline {
		view.Outline = append(view.Outline, OutlineView{Level: o.Level, Text: o.Text})
	}
	if mce.Stats != nil {
		view.ReaderWords = mce.Stats.ReaderWords
		view.ReadingSec = mce.Stats.ReadingSec
		view.ClutterPct = fmt.Sprintf("%.1f%%", mce.Stats.ClutterPct)
		view.Headlines = mce.Stats.Headlines
		if len(mce.Stats.SelectedByRole) > 0 {
			roles := make([]string, 0, len(mce.Stats.SelectedByRole))
			for role, n := range mce.Stats.SelectedByRole {
				roles = append(roles, fmt.Sprintf("%d×%s", n, role))
			}
			sort.Strings(roles)
			view.Roles = strings.Join(roles, " · ")
		}
	}
	if mce.CropBox != nil {
		view.CropBox = fmt.Sprintf("%d,%d,%d,%d", mce.CropBox[0], mce.CropBox[1], mce.CropBox[2], mce.CropBox[3])
	}

	for _, part := range mce.Reader {
		view.Reader = append(view.Reader, ReaderView{
			IsHeading: part.Role == "heading",
			IsItem:    part.Role == "listItem",
			Text:      part.Text,
		})
	}
	for _, n := range mce.Nodes {
		if n.Role == "" {
			continue
		}
		text := n.Text
		if text == "" {
			text = n.SubtreeText
		}
		if len(text) > 70 {
			text = text[:70]
		}
		roleNote := ""
		if n.Level != nil {
			roleNote = fmt.Sprintf("L%d", *n.Level)
		}
		font := ""
		if n.FontSize > 0 {
			font = fmt.Sprintf("%.0fpx", n.FontSize)
		}
		view.Nodes = append(view.Nodes, NodeView{
			IsMain: n.Selected,
			Type:   n.ContentType,
			Order:  n.Order,
			AXNode: n.AXNodeID, Role: n.Role,
			RoleNote: roleNote,
			Box:      fmt.Sprintf("%d,%d,%d,%d", n.Box[0], n.Box[1], n.Box[2], n.Box[3]),
			Font:     font,
			Text:     text,
		})
	}
}

// buildStageView fills the image stage: the embedded preview plus the
// OCR overlay layer (report.ocr_boxes) and the main-content overlay
// layer (report.mce_boxes). Both flags come from the ReportData, which
// resolves them from the settings — so a disabled layer is not merely
// hidden, it is not embedded in the file at all.
func buildStageView(view *StageView, data *pipeline.ReportData) {
	if data.PNGURI == "" || data.ImageW <= 0 || data.ImageH <= 0 {
		return
	}
	view.HasImage = true
	view.ImageURI = template.URL(data.PNGURI)
	view.ImageW, view.ImageH = data.ImageW, data.ImageH

	// OCR layer — one box per recognized line, border color encoding
	// the model's confidence for that line.
	if data.OCRBoxesOn && data.OCR != nil {
		w, h := float64(view.ImageW), float64(view.ImageH)
		for i := range data.OCR.Lines {
			ln := &data.OCR.Lines[i]
			if ln.Box == nil {
				continue
			}
			b := ln.Box
			view.OCRBoxes = append(view.OCRBoxes, BoxView{
				Style: template.CSS(fmt.Sprintf(
					"left:%.3f%%;top:%.3f%%;width:%.3f%%;height:%.3f%%;border-color:%s",
					float64(b.X)*100/w,
					float64(b.Y)*100/h,
					float64(maxInt(b.Width, 1))*100/w,
					float64(maxInt(b.Height, 1))*100/h,
					confColor(float64(ln.Confidence)))),
				Title: fmt.Sprintf("%s (conf %.2f)", ln.Text, ln.Confidence),
			})
		}
		if len(view.OCRBoxes) > 0 {
			view.HasOCR = true
			view.OCRCount = len(view.OCRBoxes)
		}
	}

	// Main-content layer — one box per Screen2x-selected node:
	// amber for headline content, green for body content, plus
	// the dashed union box (the region mce.crop_main crops).
	if data.MCEBoxesOn && data.MCE != nil && data.MCE.Ran {
		typeByAX := map[int]string{}
		for i := range data.MCE.Nodes {
			typeByAX[data.MCE.Nodes[i].AXNodeID] = data.MCE.Nodes[i].ContentType
		}
		w, h := float64(view.ImageW), float64(view.ImageH)
		for _, n := range data.MCE.Nodes {
			if !n.Selected {
				continue
			}
			if n.Box[2] <= n.Box[0] || n.Box[3] <= n.Box[1] {
				continue // degenerate / boxless node
			}
			role := n.Role
			if n.Level != nil {
				role = fmt.Sprintf("%s (L%d)", role, *n.Level)
			}
			title := role + " [" + typeByAX[n.AXNodeID] + "]"
			if text := firstNonEmpty(n.Text, n.SubtreeText); text != "" {
				title += " — " + truncateText(text, 60)
			}
			style := fmt.Sprintf(
				"left:%.3f%%;top:%.3f%%;width:%.3f%%;height:%.3f%%",
				float64(n.Box[0])*100/w,
				float64(n.Box[1])*100/h,
				float64(n.Box[2]-n.Box[0])*100/w,
				float64(n.Box[3]-n.Box[1])*100/h)
			if typeByAX[n.AXNodeID] == "headline" {
				style += ";border-color:#fdd663;background:rgba(253,214,99,.10)"
			}
			view.MCEBoxes = append(view.MCEBoxes, BoxView{
				Style: template.CSS(style),
				Title: title,
			})
		}
		if len(view.MCEBoxes) > 0 {
			view.HasMCE = true
			view.MCECount = len(view.MCEBoxes)
		}
		if cb := data.MCE.CropBox; cb != nil && (*cb)[2] > (*cb)[0] && (*cb)[3] > (*cb)[1] {
			view.HasCrop = true
			view.CropStyle = template.CSS(fmt.Sprintf(
				"left:%.3f%%;top:%.3f%%;width:%.3f%%;height:%.3f%%",
				float64((*cb)[0])*100/w,
				float64((*cb)[1])*100/h,
				float64((*cb)[2]-(*cb)[0])*100/w,
				float64((*cb)[3]-(*cb)[1])*100/h))
		}
	}
	view.HasLayers = view.HasOCR || view.HasMCE
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func confColor(conf float64) string {
	switch {
	case conf >= 0.95:
		return "#34a853"
	case conf >= 0.80:
		return "#8ab4f8"
	case conf >= 0.60:
		return "#fdd663"
	default:
		return "#f28b82"
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// WriteResults writes the .ocr.json / .mce.json (+ .report.html,
// + .reader.md and .main.png when requested) files for one result
// and returns the index entry.
func WriteResults(outdir string, res *pipeline.Result, rep *pipeline.ReportData, name string, writeHTML bool) (Entry, error) {
	stem := Stem(name)
	if err := os.MkdirAll(outdir, 0o755); err != nil {
		return Entry{}, err
	}
	entry := Entry{
		Stem: stem, Format: res.Format, Kind: string(res.Kind), Source: name,
		Elapsed: float64(res.ElapsedMS) / 1000.0,
		HasOCR:  res.OCR != nil,
		HasMCE:  res.MCE != nil && res.MCE.Ran,
		Summary: pipeline.Summarize(res),
	}
	writeJSON := func(fname string, doc any) error {
		b, err := json.MarshalIndent(doc, "", " ")
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(outdir, fname), append(b, '\n'), 0o644)
	}
	if res.OCR != nil {
		if err := writeJSON(stem+".ocr.json", res.OCR); err != nil {
			return entry, err
		}
	}
	if res.MCE != nil {
		if err := writeJSON(stem+".mce.json", res.MCE); err != nil {
			return entry, err
		}
	}
	// Reader-view export (settings key reader.format / --reader).
	if rep.ReaderView != "" && rep.ReaderFormat != "" {
		ext := pipeline.ReaderExtension(rep.ReaderFormat)
		if err := os.WriteFile(filepath.Join(outdir, stem+".reader."+ext), []byte(rep.ReaderView), 0o644); err != nil {
			return entry, err
		}
	}
	// Main-content crop (settings key mce.crop_main / --crop-main).
	if len(rep.MainPNG) > 0 {
		if err := os.WriteFile(filepath.Join(outdir, stem+".main.png"), rep.MainPNG, 0o644); err != nil {
			return entry, err
		}
	}
	if writeHTML {
		htmlName := stem + ".report.html"
		meta := MetaView{
			Kind: string(res.Kind), Format: res.Format,
			Version: rep.Version, Elapsed: fmt.Sprintf("%.1fs", rep.Elapsed),
			Source: name,
		}
		page := Build(stem, meta, rep, res)
		if err := os.WriteFile(filepath.Join(outdir, htmlName), []byte(page), 0o644); err != nil {
			return entry, err
		}
		entry.HTML = htmlName
	}
	return entry, nil
}

func renderPage(tmpl *template.Template, view any) string {
	var sb strings.Builder
	if err := tmpl.Execute(&sb, view); err != nil {
		// Templates are compile-time constants; a render failure is a
		// programming error. Render what we can rather than crash.
		return "<!doctype html><meta charset=utf-8><p>report render failed: " + template.HTMLEscapeString(err.Error())
	}
	return sb.String()
}
