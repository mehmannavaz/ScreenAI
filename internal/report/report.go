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

// BoxView is one OCR overlay box positioned in percentages of the
// image.
type BoxView struct {
	Style template.HTMLAttr
	Title string
	Color string
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

// OCRView is the OCR section view model.
type OCRView struct {
	HasImage  bool
	ImageURI  template.URL
	ImageW    int
	ImageH    int
	HasBoxes  bool
	Boxes     []BoxView
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
	Text      string
}

// OutlineView is one outline (in-content heading) row.
type OutlineView struct {
	Level int
	Text  string
}

// NodeView is one row of the MCE node table.
type NodeView struct {
	IsMain bool
	AXNode int
	Role   string
	Box    string
	Text   string
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

	if data.PNGURI != "" {
		view.HasImage = true
		view.ImageURI = template.URL(data.PNGURI)
		view.ImageW, view.ImageH = data.ImageW, data.ImageH
		if view.ImageW > 0 && view.ImageH > 0 && len(ann.Lines) > 0 {
			view.HasBoxes = true
			for i := range ann.Lines {
				ln := &ann.Lines[i]
				if ln.Box == nil {
					continue
				}
				b := ln.Box
				view.Boxes = append(view.Boxes, BoxView{
					Style: template.HTMLAttr(fmt.Sprintf(
						"left:%.3f%%;top:%.3f%%;width:%.3f%%;height:%.3f%%",
						float64(b.X)*100/float64(view.ImageW),
						float64(b.Y)*100/float64(view.ImageH),
						float64(maxInt(b.Width, 1))*100/float64(view.ImageW),
						float64(maxInt(b.Height, 1))*100/float64(view.ImageH))),
					Title: fmt.Sprintf("%s (conf %.2f)", ln.Text, ln.Confidence),
					Color: confColor(float64(ln.Confidence)),
				})
			}
		}
	}

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
	}
	if mce.CropBox != nil {
		view.CropBox = fmt.Sprintf("%d,%d,%d,%d", mce.CropBox[0], mce.CropBox[1], mce.CropBox[2], mce.CropBox[3])
	}

	selected := map[int]bool{}
	for _, ax := range mce.ContentAXNodeIDs {
		selected[ax] = true
	}
	for _, part := range mce.Reader {
		view.Reader = append(view.Reader, ReaderView{
			IsHeading: part.Role == "heading",
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
		view.Nodes = append(view.Nodes, NodeView{
			IsMain: selected[n.AXNodeID],
			AXNode: n.AXNodeID, Role: n.Role,
			Box:  fmt.Sprintf("%d,%d,%d,%d", n.Box[0], n.Box[1], n.Box[2], n.Box[3]),
			Text: text,
		})
	}
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
