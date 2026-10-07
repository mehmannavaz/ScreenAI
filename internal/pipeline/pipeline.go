package pipeline

import (
	"encoding/base64"
	"fmt"
	"image"
	"strings"
	"time"

	"github.com/mehmannavaz/ScreenAI/internal/engine"
	"github.com/mehmannavaz/ScreenAI/internal/imaging"
	"github.com/mehmannavaz/ScreenAI/internal/pagetree"
	"github.com/mehmannavaz/ScreenAI/internal/pb"
	"github.com/mehmannavaz/ScreenAI/internal/settings"
)

// Size caps shared by the CLI and the HTTP server.
const (
	// MaxPNGBase64 caps the base64 preview sent in JSON results.
	MaxPNGBase64 = 8 << 20
	// MaxEmbedImage caps images inlined into HTML reports.
	MaxEmbedImage = 6 << 20
)

// Options are the knobs shared by every pipeline run (CLI and HTTP
// server alike).
type Options struct {
	// Light selects the OCR light model.
	Light bool
	// MaxDim is an extra pre-OCR downscale cap (0 = off).
	MaxDim int
	// MCE controls whether Screen2x runs on the result.
	MCE bool
	// Languages, when non-empty, keeps only OCR lines whose detected
	// language matches (settings key ocr.languages).
	Languages []string
	// MinConfidence drops OCR lines below this confidence
	// (settings key ocr.min_confidence; 0 = keep everything).
	MinConfidence float64
	// ReaderFormat, when "text"/"markdown"/"html", renders the MCE
	// reader view into Result.ReaderView (settings key reader.format).
	ReaderFormat string
	// CropMain crops the main-content region out of the image and
	// returns it as MainPNGBase64 (settings key mce.crop_main).
	CropMain bool
	// ReportOCRBoxes embeds the OCR overlay layer in the HTML report
	// (settings key report.ocr_boxes). nil = enabled — the default.
	ReportOCRBoxes *bool
	// ReportMCEBoxes embeds the Screen2x main-content overlay layer
	// in the HTML report (settings key report.mce_boxes). nil =
	// enabled — the default.
	ReportMCEBoxes *bool
	// Tree carries the semantic-tree construction knobs (settings
	// group tree.*): heading/list/link detection and the paragraph
	// merge tolerance. Richer roles feed the model's chrome_role
	// input exactly like Chrome's own trees.
	Tree pagetree.TreeOptions
	// ExcludeRegions demotes selected nodes inside complementary
	// (aside) or contentinfo (footer) landmarks — Screen2x sometimes
	// keeps sidebar/footer text, but it never belongs in a reader
	// view (settings key mce.exclude_regions, default true).
	ExcludeRegions bool
	// Effective is the rendered settings list echoed into results
	// ("key=value" strings) so every output records how it was made.
	Effective []string
}

// ReaderPart is one entry of the MCE reader view: a top-most selected
// node with its subtree text.
type ReaderPart struct {
	Role  string `json:"role"`
	Level *int   `json:"level"`
	Text  string `json:"text"`
}

// MCENode is the JSON view of one tree node in the MCE result.
type MCENode struct {
	AXNodeID    int     `json:"axnode_id"`
	Role        string  `json:"role"`
	Text        string  `json:"text"`
	SubtreeText string  `json:"subtree_text"`
	Box         [4]int  `json:"box"`
	Level       *int    `json:"level,omitempty"`
	FontSize    float64 `json:"font_size,omitempty"`
	Selected    bool    `json:"selected"` // part of the main content?
	// ContentType is the derived per-node classification:
	// "headline" (selected and a heading or inside one), "main"
	// (selected body content) or "nonessential" (dropped as
	// clutter). The library's C API returns only the selection ids,
	// so the headline/main split is derived from the tree roles —
	// the model itself predicts three classes (CONTENT_NON_ESSENTIAL,
	// CONTENT_TEXT_HEADLINE, CONTENT_TEXT_MAIN; see
	// screen2x_config.pbtxt) but does not expose the per-class
	// scores.
	ContentType string `json:"content_type"` // headline | main | nonessential
	// Order is the reading-order position among the selected nodes
	// (1-based; 0 when not selected).
	Order int `json:"order"`
}

// MCEStats summarises what the Screen2x selection kept: how much of
// the tree is main content and how much there is to read.
type MCEStats struct {
	SelectedNodes int     `json:"selected_nodes"`
	TotalNodes    int     `json:"total_nodes"`
	KeptRatio     float64 `json:"kept_ratio"`
	ClutterPct    float64 `json:"clutter_dropped_pct"`
	ReaderWords   int     `json:"reader_words"`
	ReaderChars   int     `json:"reader_chars"`
	ReadingSec    int     `json:"reading_seconds"` // at 200 words/minute
	// Headlines is how many selected nodes are headline content
	// (headings or text inside headings).
	Headlines int `json:"headlines"`
	// SelectedByRole counts the selected nodes per chrome_role —
	// the role mix of the kept content.
	SelectedByRole map[string]int `json:"selected_by_role,omitempty"`
}

// MCE is the result of the Screen2x main-content run.
type MCE struct {
	Ran              bool          `json:"ran"`
	Elapsed          float64       `json:"elapsed"`
	ContentAXNodeIDs []int         `json:"content_axnode_ids"`
	ContentProtoIDs  []int         `json:"content_proto_ids"`
	TreeSize         [2]int        `json:"tree_size"`
	NodeCount        int           `json:"node_count"`
	Reader           []ReaderPart  `json:"reader"`
	Nodes            []MCENode     `json:"nodes"`
	Outline          []OutlineItem `json:"outline,omitempty"`
	Stats            *MCEStats     `json:"stats,omitempty"`
	CropBox          *[4]int       `json:"crop_box,omitempty"` // exact union box of the selection, image pixels

	MainNodeProtoID *int   `json:"main_node_proto_id,omitempty"`
	MainAXNodeID    *int   `json:"main_axnode_id,omitempty"`
	MainRole        string `json:"main_role,omitempty"`
	MainText        string `json:"main_text,omitempty"`
	// VetoedAXNodeIDs lists nodes Screen2x selected that the wrapper
	// demoted because they sit inside complementary/contentinfo
	// landmarks (mce.exclude_regions). The raw model selection is
	// always available in content_axnode_ids.
	VetoedAXNodeIDs []int `json:"vetoed_axnode_ids,omitempty"`
}

// Result is the JSON structure the HTTP server and the HTML reporter
// consume: one processed input.
type Result struct {
	OK            bool           `json:"ok"`
	Kind          Kind           `json:"kind"`
	Format        string         `json:"format"`
	Name          string         `json:"name"`
	Width         int            `json:"width"`
	Height        int            `json:"height"`
	ElapsedMS     int            `json:"elapsed_ms"`
	OCR           *pb.Annotation `json:"ocr,omitempty"`
	MCE           *MCE           `json:"mce,omitempty"`
	PNGBase64     string         `json:"png_base64,omitempty"`
	MainPNGBase64 string         `json:"main_png_base64,omitempty"`
	ReaderView    string         `json:"reader_view,omitempty"`
	ReaderFormat  string         `json:"reader_view_format,omitempty"`
	DownscaledTo  []int          `json:"downscaled_to,omitempty"`
	Filter        *FilterStats   `json:"filter,omitempty"`
	Settings      []string       `json:"settings,omitempty"`
}

// FilterStats reports what the language/confidence post-filters kept.
// The filtered annotation replaces the plain one in results, so these
// numbers preserve the unfiltered view.
type FilterStats struct {
	Languages     []string `json:"languages,omitempty"`
	MinConfidence float64  `json:"min_confidence,omitempty"`
	LinesBefore   int      `json:"lines_before"`
	LinesKept     int      `json:"lines_kept"`
	WordsBefore   int      `json:"words_before"`
	WordsKept     int      `json:"words_kept"`
}

// ReportData is the side-channel for the file reporter (not part of the
// JSON contract).
type ReportData struct {
	OCR          *pb.Annotation
	MCE          *MCE
	PNGURI       string // data: URI of exactly what OCR saw (images only)
	MainPNG      []byte // main-content crop PNG (mce.crop_main, images only)
	ReaderView   string // rendered reader view (reader.format)
	ReaderFormat string // "text" | "markdown" | "html"
	// OCRBoxesOn / MCEBoxesOn decide whether the HTML report embeds
	// the OCR / main-content overlay layers (report.ocr_boxes and
	// report.mce_boxes; resolved from the Options, default true).
	OCRBoxesOn bool
	MCEBoxesOn bool
	ImageW     int
	ImageH     int
	Elapsed    float64
	Version    string
}

// RunOCR decodes inp, obeys the library's size cap, and runs the real
// OCR. It returns the annotation, the (possibly downscaled) dimensions,
// and the RGBA image OCR actually saw — so callers can embed a
// pixel-true preview.
func RunOCR(s *engine.Session, inp *Input, maxDimCap int) (*pb.Annotation, *image.RGBA, error) {
	img, err := imaging.Decode(inp.Data, inp.Hint)
	if err != nil {
		return nil, nil, err
	}
	// GetMaxImageDimension() returns 0 until InitOCRUsingCallback()
	// runs, so OCR must be initialized FIRST — otherwise every image
	// would be "downscaled" to 1x1 and produce an empty result.
	if err := s.InitOCR(); err != nil {
		return nil, nil, err
	}
	limit := 0
	if maxDimCap > 0 {
		limit = maxDimCap
	}
	if libLimit := int(s.MaxImageDimension()); libLimit != 0 && (limit == 0 || libLimit < limit) {
		limit = libLimit
	}
	var downscaled []int
	img2 := imaging.ResizeMax(img, limit)
	if img2 != img {
		img = img2
		downscaled = []int{img.Bounds().Dx(), img.Bounds().Dy()}
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()

	t0 := time.Now()
	ann, err := s.PerformOCR(w, h, img.Pix)
	if err != nil {
		return nil, nil, err
	}
	ann.Width, ann.Height = w, h
	ann.Source = inp.Name
	ann.Elapsed = round3(time.Since(t0).Seconds())
	ann.DownscaledTo = downscaled
	return ann, img, nil
}

func round3(v float64) float64 {
	return float64(int(v*1000+0.5)) / 1000
}

// RunMCE runs the real Screen2x model and maps the results back to
// nodes.
//
// ExtractMainContent returns the values of the axnode_id attribute, so
// they are remapped to proto indices before Chrome's ComputeMainNode
// (LCA of the first and last selected node) is applied.
//
// excludeRegions (mce.exclude_regions, default true) demotes selected
// nodes that sit inside complementary (aside) or contentinfo (footer)
// landmark containers: Screen2x occasionally keeps sidebar ads and
// copyright strips, but they never belong in a reader view. The raw
// model selection remains in ContentAXNodeIDs / VetoedAXNodeIDs.
func RunMCE(s *engine.Session, tree *pagetree.PageTree, excludeRegions bool) (*MCE, error) {
	t0 := time.Now()
	contentIDs, err := s.ExtractMainContent(tree.Serialize())
	if err != nil {
		return nil, err
	}
	elapsed := time.Since(t0).Seconds()

	axToProto := map[int]int{}
	for i := range tree.Nodes {
		axToProto[tree.Nodes[i].Node.AXNodeID] = tree.Nodes[i].ID
	}
	protoIDs := make([]int, 0, len(contentIDs))
	for _, ax := range contentIDs {
		if p, ok := axToProto[ax]; ok {
			protoIDs = append(protoIDs, p)
		}
	}
	selected := map[int]bool{}
	for _, ax := range contentIDs {
		selected[ax] = true
	}

	// Landmark veto: demote selected nodes inside complementary /
	// contentinfo containers (see the doc comment).
	var vetoed []int
	if excludeRegions {
		inLandmark := func(n *pagetree.Node) bool {
			pid := n.ParentID
			for pid >= 0 && pid < len(tree.Nodes) {
				switch tree.Nodes[pid].Node.Role {
				case "complementary", "contentinfo":
					return true
				}
				pid = tree.Nodes[pid].Node.ParentID
			}
			return false
		}
		for i := range tree.Nodes {
			node := tree.Nodes[i].Node
			if selected[node.AXNodeID] && inLandmark(node) {
				selected[node.AXNodeID] = false
				vetoed = append(vetoed, node.AXNodeID)
			}
		}
	}
	// Final selection in tree (reading) order — drives the reader
	// view, stats, crop box and ComputeMainNode.
	finalIDs := make([]int, 0, len(protoIDs))
	for i := range tree.Nodes {
		if selected[tree.Nodes[i].Node.AXNodeID] {
			finalIDs = append(finalIDs, i)
		}
	}

	res := &MCE{
		Ran:              true,
		Elapsed:          round3(elapsed),
		ContentAXNodeIDs: contentIDs,
		ContentProtoIDs:  protoIDs,
		TreeSize:         [2]int{tree.TreeW, tree.TreeH},
		NodeCount:        len(tree.Nodes),
		Reader:           []ReaderPart{},
		Nodes:            make([]MCENode, 0, len(tree.Nodes)),
		VetoedAXNodeIDs:  vetoed,
	}

	// Reader view: top-most selected nodes — skip nodes already inside a
	// selected ancestor so no text appears twice. Selected list nodes
	// expand into one part per list item.
	//
	// Heading detection: headings are first-class roles in the semantic
	// tree now, but Screen2x may still select the TEXT nodes rather than
	// the heading containers, so a part whose nearest heading ancestor
	// (self included) is an h1..h6 renders as a heading of that level.
	// Multiple selected children of the same heading merge into one
	// part — they are one heading in the source.
	headingAncestor := func(start *pagetree.Node) (id, level int, ok bool) {
		cur := start
		for cur != nil {
			if cur.Role == "heading" {
				lvl := 1
				if cur.Level != nil {
					lvl = clamp(*cur.Level, 1, 6)
				}
				return cur.ID, lvl, true
			}
			if cur.ParentID < 0 || cur.ParentID >= len(tree.Nodes) {
				return 0, 0, false
			}
			cur = tree.Nodes[cur.ParentID].Node
		}
		return 0, 0, false
	}
	headingPart := map[int]int{} // heading node id → index in res.Reader
	addPart := func(role string, level *int, text string) {
		res.Reader = append(res.Reader, ReaderPart{Role: role, Level: level, Text: text})
	}
	expandList := func(node *pagetree.Node) {
		node.Walk(func(n *pagetree.Node) {
			if n.Role == "listItem" {
				if text := pagetree.SubtreeText(n); text != "" {
					addPart("listItem", nil, text)
				}
			}
		})
	}
	for i := range tree.Nodes {
		node := tree.Nodes[i].Node
		if !selected[node.AXNodeID] {
			continue
		}
		inside := false
		pid := node.ParentID
		for pid >= 0 && pid < len(tree.Nodes) {
			pnode := tree.Nodes[pid].Node
			if selected[pnode.AXNodeID] {
				inside = true
				break
			}
			pid = pnode.ParentID
		}
		if inside {
			continue
		}
		if node.Role == "list" {
			expandList(node)
			continue
		}
		text := pagetree.SubtreeText(node)
		if text == "" {
			continue
		}
		if node.Role == "listItem" {
			addPart("listItem", nil, text)
			continue
		}
		if hid, lvl, isHeading := headingAncestor(node); isHeading {
			if idx, seen := headingPart[hid]; seen {
				res.Reader[idx].Text = strings.TrimSpace(res.Reader[idx].Text + " " + text)
				continue
			}
			headingPart[hid] = len(res.Reader)
			res.Reader = append(res.Reader, ReaderPart{Role: "heading", Level: &lvl, Text: text})
			continue
		}
		res.Reader = append(res.Reader, ReaderPart{Role: node.Role, Level: node.Level, Text: text})
	}

	for i := range tree.Nodes {
		node := tree.Nodes[i].Node
		fontSize := 0.0
		if node.FontSize != nil {
			fontSize = *node.FontSize
		}
		res.Nodes = append(res.Nodes, MCENode{
			AXNodeID:    node.AXNodeID,
			Role:        node.Role,
			Text:        node.Text,
			SubtreeText: pagetree.SubtreeText(node),
			Box:         tree.Nodes[i].BoxPixels,
			Level:       node.Level,
			FontSize:    round1(fontSize),
			Selected:    selected[node.AXNodeID],
		})
	}

	// Derived per-node content types and reading order. The model
	// predicts three classes (non-essential / headline / main) but the
	// C API exposes only the selection, so headline content is derived
	// from the heading roles of the semantic tree.
	headlineAX := map[int]bool{}
	for i := range tree.Nodes {
		node := tree.Nodes[i].Node
		if !selected[node.AXNodeID] {
			continue
		}
		if _, _, isHeading := headingAncestor(node); isHeading {
			headlineAX[node.AXNodeID] = true
		}
	}
	next := 1
	for i := range res.Nodes {
		n := &res.Nodes[i]
		switch {
		case !n.Selected:
			n.ContentType = "nonessential"
		case headlineAX[n.AXNodeID]:
			n.ContentType = "headline"
		default:
			n.ContentType = "main"
		}
		if n.Selected {
			n.Order = next
			next++
		}
	}

	// Outline: the headings of the reader view, in reading order —
	// exactly the structure a markdown/html export will emit.
	for i := range res.Reader {
		if res.Reader[i].Role != "heading" {
			continue
		}
		level := 1
		if res.Reader[i].Level != nil {
			level = clamp(*res.Reader[i].Level, 1, 6)
		}
		res.Outline = append(res.Outline, OutlineItem{Level: level, Text: oneLine(res.Reader[i].Text)})
	}

	// Stats: how much of the tree is main content, how much to read.
	readerWords, readerChars := 0, 0
	for _, part := range res.Reader {
		readerWords += len(strings.Fields(part.Text))
		readerChars += len(part.Text)
	}
	total := len(tree.Nodes)
	if total > 0 {
		s := &MCEStats{
			SelectedNodes:  len(finalIDs),
			TotalNodes:     total,
			KeptRatio:      float64(len(finalIDs)) / float64(total),
			ReaderWords:    readerWords,
			ReaderChars:    readerChars,
			ReadingSec:     (readerWords*60 + ReadingWordsPerMinute - 1) / ReadingWordsPerMinute,
			SelectedByRole: map[string]int{},
		}
		for i := range res.Nodes {
			n := &res.Nodes[i]
			if !n.Selected {
				continue
			}
			s.SelectedByRole[n.Role]++
			if n.ContentType == "headline" {
				s.Headlines++
			}
		}
		s.ClutterPct = round1((1 - s.KeptRatio) * 100)
		s.KeptRatio = round3(s.KeptRatio)
		res.Stats = s
	}

	// Exact union box of the selection, in image pixels — what
	// mce.crop_main crops out (padded slightly at crop time).
	if len(finalIDs) > 0 {
		var box *[4]int
		for _, p := range finalIDs {
			if p < 0 || p >= len(tree.Nodes) {
				continue
			}
			nb := tree.Nodes[p].BoxPixels
			if nb[2] <= nb[0] || nb[3] <= nb[1] {
				continue // degenerate/no box
			}
			if box == nil {
				b := nb
				box = &b
				continue
			}
			box[0] = minInt(box[0], nb[0])
			box[1] = minInt(box[1], nb[1])
			box[2] = maxInt(box[2], nb[2])
			box[3] = maxInt(box[3], nb[3])
		}
		res.CropBox = box
	}

	if len(finalIDs) > 0 {
		mainProto, _ := tree.ComputeMainNode(finalIDs)
		node := tree.Nodes[mainProto].Node
		res.MainNodeProtoID = &mainProto
		ax := node.AXNodeID
		res.MainAXNodeID = &ax
		res.MainRole = node.Role
		res.MainText = firstText(tree, mainProto)
	}
	return res, nil
}

func firstText(tree *pagetree.PageTree, protoID int) string {
	node := tree.Nodes[protoID].Node
	var text string
	node.Walk(func(n *pagetree.Node) {
		if text == "" && n.Text != "" {
			text = n.Text
		}
	})
	return text
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func round1(v float64) float64 {
	return float64(int(v*10+0.5)) / 10
}

// TreeForInput builds the page tree for non-image inputs.
func TreeForInput(inp *Input, treeOpts pagetree.TreeOptions) (*pagetree.PageTree, error) {
	text := string(inp.Data)
	switch inp.Kind {
	case KindPage:
		return pagetree.FromHTML(text, 1280), nil
	case KindTree:
		return pagetree.FromJSON(inp.Data, treeOpts)
	default:
		return pagetree.FromText(text, 800, treeOpts), nil
	}
}

// Process runs the right pipeline on one input through an open engine
// session. The caller owns the engine lock (engine.Session) — the CLI
// and the HTTP server both hold it for the whole pipeline so a
// light-mode switch cannot interleave with other callers.
func Process(s *engine.Session, inp *Input, opts Options) (*Result, *ReportData, error) {
	start := time.Now()
	res := &Result{OK: true, Kind: inp.Kind, Format: inp.Format, Name: inp.Name, Settings: opts.Effective}
	rep := &ReportData{
		// Overlay layers default to enabled; a nil option means
		// "not configured" = keep the default.
		OCRBoxesOn: opts.ReportOCRBoxes == nil || *opts.ReportOCRBoxes,
		MCEBoxesOn: opts.ReportMCEBoxes == nil || *opts.ReportMCEBoxes,
	}

	if opts.Light != s.OCRLightMode() {
		s.SetOCRLightMode(opts.Light)
	}

	var pipelineErr error
	if inp.Kind == KindImage {
		ann, img, err := RunOCR(s, inp, opts.MaxDim)
		if err != nil {
			pipelineErr = err
		} else {
			// Post-filters (target language / minimum confidence)
			// are applied BEFORE the MCE tree is built, so the
			// reader view honors them.
			ann, res.Filter = FilterAnnotation(ann, opts.Languages, opts.MinConfidence)
			res.OCR = ann
			rep.OCR = ann
			res.Width, res.Height = ann.Width, ann.Height
			res.DownscaledTo = ann.DownscaledTo
			rep.ImageW, rep.ImageH = ann.Width, ann.Height

			// The preview is a genuine PNG of exactly what OCR saw — the
			// raw bytes of a BMP/JPEG/TGA input are not a valid PNG.
			if png, err := imaging.EncodePNG(img); err == nil {
				if len(png) > 0 && len(png) <= MaxPNGBase64 {
					res.PNGBase64 = base64.StdEncoding.EncodeToString(png)
				}
				if len(png) > 0 && len(png) <= MaxEmbedImage {
					rep.PNGURI = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
				}
			}

			if opts.MCE {
				tree := pagetree.FromOCR(ann, img, ann.Width, ann.Height, opts.Tree)
				mce, err := RunMCE(s, tree, opts.ExcludeRegions)
				if err != nil {
					pipelineErr = err
				} else {
					res.MCE, rep.MCE = mce, mce
					finishMCEDerived(res, rep, mce, opts)
					// The crop needs the image the OCR saw, so it can only
					// happen on the image path — boxes line up pixel-exact.
					if opts.CropMain && mce.CropBox != nil {
						// Crop failures (degenerate boxes) are
						// non-fatal: the run still succeeds, the
						// crop file is simply not produced.
						if png, cerr := CropMainContent(img, mce); cerr == nil {
							rep.MainPNG = png
							if len(png) <= MaxPNGBase64 {
								res.MainPNGBase64 = base64.StdEncoding.EncodeToString(png)
							}
						}
					}
				}
			}
		}
	} else {
		tree, err := TreeForInput(inp, opts.Tree)
		if err != nil {
			pipelineErr = err
		} else {
			mce, err := RunMCE(s, tree, opts.ExcludeRegions)
			if err != nil {
				pipelineErr = err
			} else {
				res.MCE, rep.MCE = mce, mce
				finishMCEDerived(res, rep, mce, opts)
				res.Width, res.Height = tree.TreeW, tree.TreeH
			}
		}
	}

	res.ElapsedMS = int(time.Since(start).Milliseconds())
	rep.Elapsed = time.Since(start).Seconds()
	rep.Version = s.VersionString()
	if pipelineErr != nil {
		return res, rep, pipelineErr
	}
	return res, rep, nil
}

// finishMCEDerived renders the reader-view export (reader.format)
// shared by both the image and the document paths. The main-content
// crop is image-only and handled where the pixels are in scope.
func finishMCEDerived(res *Result, rep *ReportData, mce *MCE, opts Options) {
	if f, err := settings.NormalizeReaderFormat(opts.ReaderFormat); err == nil && (f == "text" || f == "markdown" || f == "html") {
		res.ReaderView = RenderReaderView(mce, f)
		res.ReaderFormat = f
		rep.ReaderView, rep.ReaderFormat = res.ReaderView, f
	}
}

// FilterAnnotation applies the language and confidence post-filters to
// a copy of the annotation and returns the copy plus statistics. nil is
// returned as-is (nothing to filter). Language matching is exact on the
// model's own per-line language id (ISO 639 codes such as "en"); lines
// without a detected language are kept only when no language filter is
// set. The input annotation is never mutated.
func FilterAnnotation(ann *pb.Annotation, languages []string, minConf float64) (*pb.Annotation, *FilterStats) {
	if ann == nil || (len(languages) == 0 && minConf <= 0) {
		return ann, nil
	}
	want := map[string]bool{}
	for _, l := range languages {
		want[strings.ToLower(strings.TrimSpace(l))] = true
	}
	out := &pb.Annotation{
		Width:        ann.Width,
		Height:       ann.Height,
		Source:       ann.Source,
		Elapsed:      ann.Elapsed,
		DownscaledTo: ann.DownscaledTo,
		Lines:        make([]pb.Line, 0, len(ann.Lines)),
	}
	for _, ln := range ann.Lines {
		if len(want) > 0 && !want[strings.ToLower(ln.Language)] {
			continue
		}
		if minConf > 0 && ln.Confidence > 0 && float64(ln.Confidence) < minConf {
			continue
		}
		out.Lines = append(out.Lines, ln)
	}
	stats := &FilterStats{
		Languages:     languages,
		MinConfidence: minConf,
		LinesBefore:   len(ann.Lines),
		LinesKept:     len(out.Lines),
		WordsBefore:   ann.WordCount(),
		WordsKept:     out.WordCount(),
	}
	return out, stats
}

// Summarize renders a one-line human summary of a Process result.
func Summarize(res *Result) string {
	var parts []string
	if res.OCR != nil {
		parts = append(parts, fmt.Sprintf("OCR: %d lines / %d words", len(res.OCR.Lines), res.OCR.WordCount()))
	}
	if res.MCE != nil && res.MCE.Ran {
		role := "?"
		if res.MCE.MainRole != "" {
			role = res.MCE.MainRole
		}
		n := len(res.MCE.ContentAXNodeIDs)
		if res.MCE.Stats != nil {
			n = res.MCE.Stats.SelectedNodes
		}
		parts = append(parts, fmt.Sprintf("main content: %d/%d nodes (%s)", n, res.MCE.NodeCount, role))
	}
	if len(parts) == 0 {
		return "no results"
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += " · " + p
	}
	return out
}
