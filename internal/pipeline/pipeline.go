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
	AXNodeID    int    `json:"axnode_id"`
	Role        string `json:"role"`
	Text        string `json:"text"`
	SubtreeText string `json:"subtree_text"`
	Box         [4]int `json:"box"`
	Level       *int   `json:"level"`
}

// MCE is the result of the Screen2x main-content run.
type MCE struct {
	Ran              bool         `json:"ran"`
	Elapsed          float64      `json:"elapsed"`
	ContentAXNodeIDs []int        `json:"content_axnode_ids"`
	ContentProtoIDs  []int        `json:"content_proto_ids"`
	TreeSize         [2]int       `json:"tree_size"`
	NodeCount        int          `json:"node_count"`
	Reader           []ReaderPart `json:"reader"`
	Nodes            []MCENode    `json:"nodes"`

	MainNodeProtoID *int   `json:"main_node_proto_id,omitempty"`
	MainAXNodeID    *int   `json:"main_axnode_id,omitempty"`
	MainRole        string `json:"main_role,omitempty"`
	MainText        string `json:"main_text,omitempty"`
}

// Result is the JSON structure the HTTP server and the HTML reporter
// consume: one processed input.
type Result struct {
	OK           bool           `json:"ok"`
	Kind         Kind           `json:"kind"`
	Format       string         `json:"format"`
	Name         string         `json:"name"`
	Width        int            `json:"width"`
	Height       int            `json:"height"`
	ElapsedMS    int            `json:"elapsed_ms"`
	OCR          *pb.Annotation `json:"ocr,omitempty"`
	MCE          *MCE           `json:"mce,omitempty"`
	PNGBase64    string         `json:"png_base64,omitempty"`
	DownscaledTo []int          `json:"downscaled_to,omitempty"`
	Filter       *FilterStats   `json:"filter,omitempty"`
	Settings     []string       `json:"settings,omitempty"`
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
	OCR     *pb.Annotation
	MCE     *MCE
	PNGURI  string // data: URI of exactly what OCR saw (images only)
	ImageW  int
	ImageH  int
	Elapsed float64
	Version string
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
func RunMCE(s *engine.Session, tree *pagetree.PageTree) (*MCE, error) {
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

	res := &MCE{
		Ran:              true,
		Elapsed:          round3(elapsed),
		ContentAXNodeIDs: contentIDs,
		ContentProtoIDs:  protoIDs,
		TreeSize:         [2]int{tree.TreeW, tree.TreeH},
		NodeCount:        len(tree.Nodes),
		Reader:           []ReaderPart{},
		Nodes:            make([]MCENode, 0, len(tree.Nodes)),
	}

	// Reader view: top-most selected nodes — skip nodes already inside a
	// selected ancestor so no text appears twice.
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
		if text := pagetree.SubtreeText(node); text != "" {
			res.Reader = append(res.Reader, ReaderPart{Role: node.Role, Level: node.Level, Text: text})
		}
	}

	for i := range tree.Nodes {
		node := tree.Nodes[i].Node
		res.Nodes = append(res.Nodes, MCENode{
			AXNodeID:    node.AXNodeID,
			Role:        node.Role,
			Text:        node.Text,
			SubtreeText: pagetree.SubtreeText(node),
			Box:         tree.Nodes[i].BoxPixels,
			Level:       node.Level,
		})
	}

	if len(protoIDs) > 0 {
		mainProto, _ := tree.ComputeMainNode(protoIDs)
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

// TreeForInput builds the page tree for non-image inputs.
func TreeForInput(inp *Input) (*pagetree.PageTree, error) {
	text := string(inp.Data)
	switch inp.Kind {
	case KindPage:
		return pagetree.FromHTML(text, 1280), nil
	case KindTree:
		return pagetree.FromJSON(inp.Data)
	default:
		return pagetree.FromText(text, 800), nil
	}
}

// Process runs the right pipeline on one input through an open engine
// session. The caller owns the engine lock (engine.Session) — the CLI
// and the HTTP server both hold it for the whole pipeline so a
// light-mode switch cannot interleave with other callers.
func Process(s *engine.Session, inp *Input, opts Options) (*Result, *ReportData, error) {
	start := time.Now()
	res := &Result{OK: true, Kind: inp.Kind, Format: inp.Format, Name: inp.Name, Settings: opts.Effective}
	rep := &ReportData{}

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
				tree := pagetree.FromOCR(ann, ann.Width, ann.Height)
				mce, err := RunMCE(s, tree)
				if err != nil {
					pipelineErr = err
				} else {
					res.MCE, rep.MCE = mce, mce
				}
			}
		}
	} else {
		tree, err := TreeForInput(inp)
		if err != nil {
			pipelineErr = err
		} else {
			mce, err := RunMCE(s, tree)
			if err != nil {
				pipelineErr = err
			} else {
				res.MCE, rep.MCE = mce, mce
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
		parts = append(parts, fmt.Sprintf("main content: %d/%d nodes (%s)", len(res.MCE.ContentAXNodeIDs), res.MCE.NodeCount, role))
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
