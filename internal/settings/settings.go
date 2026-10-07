// Package settings exposes every tunable knob of Google's Screen AI
// component that a wrapper can reach, and rewrites the component's own
// configuration files on the fly so the closed-source binary itself
// picks the values up.
//
// # Where the knobs live
//
// Deep analysis of the component (v153.1) shows distinct layers of
// configuration, each reached through a different mechanism:
//
//  1. The public C API: SetOCRLightMode, EnableDebugMode — direct calls.
//  2. screen2x_config.pbtxt: the Screen2x main-content model config,
//     including the class thresholds that decide how aggressively
//     clutter is filtered.
//  3. The OCR engine graph (gocr_..._engine.binarypb): a MediaPipe
//     graph with template parameters (interpreter pool size,
//     max-in-flight, ...). We rewrite the parameter defaults.
//  4. The script registry (gocr_line_recognition_..._q4.binarypb): the
//     script → recognizer-model map (Arab, Cyrl, Jpan, ...). Restricting
//     it is the "target language" control: only the kept scripts' models
//     load and run.
//  5. The text detection config (gocr_group_rpn_text_detection_config_
//     ..._chrome.binarypb): a protobuf with detection floats, patched by
//     field number.
//
// Layers 2-5 are all served to the library through the
// SetFileContentFunctions virtual file system, so the wrapper can
// intercept and rewrite them before the library ever sees them —
// verified live against the real binary:
//
//	mce.threshold_non_essential 0.65→0.01 : 8→7 selected nodes
//	mce.threshold_non_essential 0.65→0.99 : 8→15 selected nodes
//	ocr.detection_score_threshold 0.5→0.9 : 25→28 OCR lines
//	ocr.scripts=all → {und,Grek,Jpan,Kore,Hani} : OCR unchanged, fewer models
//
// # What does not exist
//
// There is no top-k, temperature, or "set output language" function in
// the library's public surface: the models are deterministic. Target
// languages are expressed through ocr.scripts (pipeline restriction)
// and ocr.languages (result filtering by the model's own per-line
// language detection). Graph template booleans (use_line_selection,
// use_aksara_layout, ...) exist in the engine config but flipping them
// makes InitOCR fail — Google does not ship the alternative sub-models
// they gate, so they are deliberately not exposed here.
package settings

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mehmannavaz/ScreenAI/internal/pagetree"
)

// Mechanism describes how a setting reaches the closed-source library.
type Mechanism string

const (
	// MechanismCAPI is a direct call into the library's exported C API.
	MechanismCAPI Mechanism = "c-api"
	// MechanismPbtxt rewrites screen2x_config.pbtxt (served via the VFS).
	MechanismPbtxt Mechanism = "pbtxt"
	// MechanismGraphParam rewrites a template parameter default inside
	// the OCR engine graph binarypb.
	MechanismGraphParam Mechanism = "graph-param"
	// MechanismRegistry rewrites the script → recognizer registry.
	MechanismRegistry Mechanism = "script-registry"
	// MechanismDetection rewrites a float in the detection config.
	MechanismDetection Mechanism = "detection-config"
	// MechanismWrapper is applied wrapper-side (pre/post-processing).
	MechanismWrapper Mechanism = "wrapper"
)

// Descriptor documents one settings key.
type Descriptor struct {
	Key         string
	Type        string // "bool", "int", "float", "list"
	Default     string // stock value inside the component as shipped
	Mechanism   Mechanism
	Summary     string
	Effect      string // observed behaviour when changed (live-verified)
	engineParam string // internal: template parameter name, when applicable
}

// Settings is the full, user-facing configuration. Nil pointers and
// empty slices mean "leave the component's stock value untouched".
//
// Values are engine-lifetime constants: the library reads its config
// files during InitOCR and caches them, so changes require a fresh
// engine. The wrapper-side fields (Languages, MinConfidence) are
// per-run.
type Settings struct {
	// ---- layer 1: public C API ----
	OCRLightMode *bool // SetOCRLightMode: swap full ↔ light OCR model
	DebugMode    *bool // EnableDebugMode: dump i/o protos to the temp dir

	// ---- layer 3: OCR engine graph parameters ----
	TFLitePoolSize *int // tflite_interpreter_pool_min_size (stock 4)
	MaxInFlight    *int // max_in_flight (stock 1)

	// ---- layer 4: script registry restriction ----
	// Scripts keeps only these script recognizers. Accepts ISO 15924
	// script codes (Arab, Cyrl, Hani, ...) or language codes (en, ru,
	// ja, ...) which are mapped to scripts. 'und' (the Latin/universal
	// fallback) is always kept — removing it breaks every script.
	Scripts []string

	// ---- layer 5: text detection config ----
	DetectionScoreThreshold *float64

	// ---- layer 2: screen2x (MCE) config ----
	MCEThresholdNonEssential *float64 // stock 0.65 — the effective clutter gate
	MCEThresholdHeadline     *float64 // stock 0.0
	MCEThresholdMain         *float64 // stock 0.0
	MCENumThreads            *int     // stock 1
	MCEChromePostProcess     *bool    // stock true

	// ---- MCE-derived output options (wrapper-side, per-run) ----
	// CropMain crops the union box of the selected main-content
	// nodes out of the image the OCR saw and saves it as
	// <stem>.main.png (images only). See mce.crop_main.
	CropMain bool
	// ExcludeRegions demotes Screen2x selections inside complementary
	// (aside) / contentinfo (footer) landmarks. nil = default true.
	// See mce.exclude_regions.
	ExcludeRegions *bool

	// ---- reader-view export (wrapper-side, per-run) ----
	// ReaderFormat exports the MCE reader view as a file:
	// "", "none" (off), "text", "markdown" or "html".
	// See reader.format.
	ReaderFormat string

	// ---- HTML report overlays (wrapper-side, per-run) ----
	// ReportOCRBoxes embeds the OCR line-box overlay layer in the
	// HTML report; ReportMCEBoxes embeds the main-content (Screen2x)
	// overlay layer. nil = default true. See report.ocr_boxes /
	// report.mce_boxes.
	ReportOCRBoxes *bool
	ReportMCEBoxes *bool

	// ---- semantic tree construction (wrapper-side, per-run) ----
	// The tree.* knobs control how much structure the wrapper infers
	// from the OCR output before Screen2x sees it. Chrome's own trees
	// always carry heading/link/list roles, and the model has a
	// dedicated categorical chrome_role input — so richer roles mean
	// better main-content decisions. nil = stock defaults.
	TreeDetectHeadings  *bool    // tree.detect_headings
	TreeDetectLists     *bool    // tree.detect_lists
	TreeDetectLinks     *bool    // tree.detect_links
	TreeDetectRegions   *bool    // tree.detect_regions
	TreeHeadingMinRatio *float64 // tree.heading_min_ratio
	TreeParagraphGap    *float64 // tree.paragraph_gap

	// ---- wrapper-side pre-processing ----
	MaxImageDim int  // extra pre-OCR downscale cap, 0 = off
	MCEEnabled  bool // run Screen2x at all
	MCEExplicit bool // whether MCEEnabled was user-set

	// ---- wrapper-side post-filters (per-run) ----
	// Languages keeps only OCR lines whose detected language matches
	// (e.g. "en,de"). Applied before MCE so the reader view honors it.
	Languages []string
	// MinConfidence drops OCR lines below this confidence [0,1].
	MinConfidence float64
}

// Default returns the stock Settings describing the component exactly
// as Google ships it (nothing overridden).
func Default() *Settings { return &Settings{MCEEnabled: true} }

// NonEmpty reports whether any engine-level override is active
// (anything that changes the bytes served to the library).
func (s *Settings) NonEmpty() bool {
	if s == nil {
		return false
	}
	return s.TFLitePoolSize != nil ||
		s.MaxInFlight != nil ||
		len(s.Scripts) > 0 ||
		s.DetectionScoreThreshold != nil ||
		s.MCEThresholdNonEssential != nil ||
		s.MCEThresholdHeadline != nil ||
		s.MCEThresholdMain != nil ||
		s.MCENumThreads != nil ||
		s.MCEChromePostProcess != nil
}

// Descriptors is the complete settings reference, in stable order.
var Descriptors = []Descriptor{
	// OCR
	{
		Key: "ocr.light_mode", Type: "bool", Default: "false",
		Mechanism: MechanismCAPI,
		Summary:   "Use the light (faster, less accurate) OCR model",
		Effect:    "Trades accuracy for speed; switches via SetOCRLightMode and reloads the pipeline",
	},
	{
		Key: "ocr.debug_mode", Type: "bool", Default: "false",
		Mechanism: MechanismCAPI,
		Summary:   "Library debug mode: dumps all i/o protos to the temp dir",
		Effect:    "Produces debug artifacts; no effect on results",
	},
	{
		Key: "ocr.scripts", Type: "list", Default: "all 21",
		Mechanism: MechanismRegistry,
		Summary:   "Restrict loaded scripts/recognizers — the target-language control",
		Effect:    "Verified: {und,Grek,Jpan,Kore,Hani} keeps English OCR identical while skipping 16 models",
	},
	{
		Key: "ocr.languages", Type: "list", Default: "(all)",
		Mechanism: MechanismWrapper,
		Summary:   "Keep only OCR lines detected as these languages (en, ja, ...)",
		Effect:    "Post-filter using the model's own per-line language id; applied before MCE",
	},
	{
		Key: "ocr.min_confidence", Type: "float", Default: "0",
		Mechanism: MechanismWrapper,
		Summary:   "Drop OCR lines below this confidence [0, 1]",
		Effect:    "Post-filter; 0.8 keeps only high-confidence lines",
	},
	{
		Key: "ocr.max_image_dimension", Type: "int", Default: "0 (library caps at 2048)",
		Mechanism: MechanismWrapper,
		Summary:   "Extra pre-OCR downscale cap in pixels",
		Effect:    "maxdim=800 downscales a 1600px image to 800px before OCR (faster, less detail)",
	},
	{
		Key: "ocr.tflite_interpreter_pool_min_size", Type: "int", Default: "4",
		Mechanism:   MechanismGraphParam,
		Summary:     "Minimum TFLite interpreter pool size (OCR graph parameter)",
		Effect:      "Verified: accepted by the graph; performance knob for parallel inference",
		engineParam: "tflite_interpreter_pool_min_size",
	},
	{
		Key: "ocr.max_in_flight", Type: "int", Default: "1",
		Mechanism:   MechanismGraphParam,
		Summary:     "Maximum requests in flight through the OCR graph",
		Effect:      "Verified: accepted; raise together with the pool size for throughput",
		engineParam: "max_in_flight",
	},
	{
		Key: "ocr.detection_score_threshold", Type: "float", Default: "0.5",
		Mechanism: MechanismDetection,
		Summary:   "Text-region detection sensitivity (post-processing float)",
		Effect:    "Verified: 0.5→0.9 changed 25→28 OCR lines on the news example; tune per input",
	},
	// MCE
	{
		Key: "mce.enabled", Type: "bool", Default: "true",
		Mechanism: MechanismWrapper,
		Summary:   "Run Screen2x main-content extraction at all",
		Effect:    "false = images run OCR only",
	},
	{
		Key: "mce.threshold_non_essential", Type: "float", Default: "0.65",
		Mechanism: MechanismPbtxt,
		Summary:   "Screen2x clutter gate: P(non-essential) above this = dropped",
		Effect:    "Verified: 0.01 → 7 of 41 nodes kept, 0.65 → 8, 0.99 → 15; the aggressive/lenient dial",
	},
	{
		Key: "mce.threshold_headline", Type: "float", Default: "0.0",
		Mechanism: MechanismPbtxt,
		Summary:   "Screen2x headline class threshold",
		Effect:    "Verified accepted; on tested inputs selection is driven by the non-essential gate",
	},
	{
		Key: "mce.threshold_main", Type: "float", Default: "0.0",
		Mechanism: MechanismPbtxt,
		Summary:   "Screen2x main-content class threshold",
		Effect:    "Verified accepted; combine with the non-essential gate for fine control",
	},
	{
		Key: "mce.num_threads", Type: "int", Default: "1",
		Mechanism: MechanismPbtxt,
		Summary:   "Screen2x TFLite inference threads",
		Effect:    "Verified accepted; performance only (the model is deterministic)",
	},
	{
		Key: "mce.chrome_post_process", Type: "bool", Default: "true",
		Mechanism: MechanismPbtxt,
		Summary:   "Chrome-specific Screen2x post-processing",
		Effect:    "Verified accepted; effect depends on the tree shape",
	},
	{
		Key: "mce.exclude_regions", Type: "bool", Default: "true",
		Mechanism: MechanismWrapper,
		Summary:   "Demote selections inside aside/footer landmarks (complementary, contentinfo)",
		Effect:    "Keeps sidebar ads and copyright strips out of the reader view; the raw model selection stays in content_axnode_ids / vetoed_axnode_ids",
	},
	{
		Key: "mce.crop_main", Type: "bool", Default: "false",
		Mechanism: MechanismWrapper,
		Summary:   "Crop the main-content region out of the input image",
		Effect:    "Images only: writes <stem>.main.png (union box of the selected nodes) and sets crop_box in the MCE JSON",
	},
	// Reader view (MCE-derived output)
	{
		Key: "reader.format", Type: "string", Default: "none",
		Mechanism: MechanismWrapper,
		Summary:   "Export the MCE reader view as text, markdown or html",
		Effect:    "markdown writes <stem>.reader.md with heading levels from the tree; the reader view is distilled from the Screen2x selection",
	},
	// Report (HTML output)
	{
		Key: "report.ocr_boxes", Type: "bool", Default: "true",
		Mechanism: MechanismWrapper,
		Summary:   "Highlight what OCR found: line boxes over the image in the HTML report",
		Effect:    "false omits the OCR overlay layer entirely (not merely hidden); the image itself is still embedded",
	},
	{
		Key: "report.mce_boxes", Type: "bool", Default: "true",
		Mechanism: MechanismWrapper,
		Summary:   "Highlight what MCE found: Screen2x main-content boxes over the image",
		Effect:    "false omits the green main-content overlay layer and the dashed region box; --crop-main still works",
	},
	// Semantic tree construction
	{
		Key: "tree.detect_headings", Type: "bool", Default: "true",
		Mechanism: MechanismWrapper,
		Summary:   "Mark clearly larger OCR lines as role heading with a hierarchical_level",
		Effect:    "Feeds the Screen2x chrome_role input like Chrome's trees; the reader view and outline gain headings on image inputs",
	},
	{
		Key: "tree.detect_lists", Type: "bool", Default: "true",
		Mechanism: MechanismWrapper,
		Summary:   "Group bullet/numbered lines and same-row menu runs into list/listItem nodes",
		Effect:    "Nav bars and bullet lists appear as lists; list items render as bullets in the reader view",
	},
	{
		Key: "tree.detect_links", Type: "bool", Default: "true",
		Mechanism: MechanismWrapper,
		Summary:   "Mark link-inked lines (engine foreground_rgb) or underlined lines as role link",
		Effect:    "Uses the engine's own per-word ink colour plus a pixel underline pass; blue nav/body links become link nodes",
	},
	{
		Key: "tree.detect_regions", Type: "bool", Default: "true",
		Mechanism: MechanismWrapper,
		Summary:   "Wrap side columns as complementary (aside) and the trailing footer as contentinfo",
		Effect:    "Landmark roles are classic clutter signals: on the news example the sidebar ad and copyright line stay out of the reader view",
	},
	{
		Key: "tree.heading_min_ratio", Type: "float", Default: "1.3",
		Mechanism: MechanismWrapper,
		Summary:   "Height ratio over body text at which a line becomes a heading",
		Effect:    "1.2 is more eager (bold sub-headers become headings); 1.6 only clear display sizes",
	},
	{
		Key: "tree.paragraph_gap", Type: "float", Default: "0.75",
		Mechanism: MechanismWrapper,
		Summary:   "Max vertical gap (× body line height) that still merges lines into one paragraph",
		Effect:    "Higher merges more aggressively (multi-block paragraphs); lower splits strictly per OCR block",
	},
}

// Lookup returns the descriptor for key (exact match).
func Lookup(key string) (Descriptor, bool) {
	for _, d := range Descriptors {
		if d.Key == key {
			return d, true
		}
	}
	return Descriptor{}, false
}

// Suggestions returns the closest known keys to a misspelled one.
func Suggestions(key string) []string {
	var out []string
	for _, d := range Descriptors {
		if strings.Contains(d.Key, key) || strings.Contains(key, d.Key) ||
			levenshtein(d.Key, key) <= 4 {
			out = append(out, d.Key)
		}
	}
	sort.Strings(out)
	return out
}

// Set applies one "key=value" string from --set / the JSON config /
// query parameters. Booleans take true/false/1/0; lists are
// comma-separated.
func (s *Settings) Set(key, value string) error {
	if _, ok := Lookup(key); !ok {
		if sugg := Suggestions(key); len(sugg) > 0 {
			return fmt.Errorf("unknown setting %q (did you mean: %s?)", key, strings.Join(sugg, ", "))
		}
		return fmt.Errorf("unknown setting %q — see `screenai settings`", key)
	}
	switch key {
	case "ocr.light_mode":
		return setBool(&s.OCRLightMode, key, value)
	case "ocr.debug_mode":
		return setBool(&s.DebugMode, key, value)
	case "ocr.scripts":
		scripts, err := ParseScripts(strings.Split(value, ","))
		if err != nil {
			return fmt.Errorf("%s: %v", key, err)
		}
		s.Scripts = scripts
		return nil
	case "ocr.languages":
		langs, err := ParseLanguages(strings.Split(value, ","))
		if err != nil {
			return fmt.Errorf("%s: %v", key, err)
		}
		s.Languages = langs
		return nil
	case "ocr.min_confidence":
		f, err := parseFloat(key, value, 0, 1)
		if err != nil {
			return err
		}
		s.MinConfidence = *f
		return nil
	case "ocr.max_image_dimension":
		n, err := parseInt(key, value, 0, 1<<30)
		if err != nil {
			return err
		}
		s.MaxImageDim = int(n)
		return nil
	case "ocr.tflite_interpreter_pool_min_size":
		n, err := parseInt(key, value, 1, 256)
		if err != nil {
			return err
		}
		s.TFLitePoolSize = ptr(int(n))
		return nil
	case "ocr.max_in_flight":
		n, err := parseInt(key, value, 1, 256)
		if err != nil {
			return err
		}
		s.MaxInFlight = ptr(int(n))
		return nil
	case "ocr.detection_score_threshold":
		f, err := parseFloat(key, value, 0, 1)
		if err != nil {
			return err
		}
		s.DetectionScoreThreshold = f
		return nil
	case "mce.enabled":
		b, err := parseBool(value)
		if err != nil {
			return fmt.Errorf("%s: %v", key, err)
		}
		s.MCEEnabled, s.MCEExplicit = b, true
		return nil
	case "mce.threshold_non_essential":
		f, err := parseFloat(key, value, 0, 1)
		if err != nil {
			return err
		}
		s.MCEThresholdNonEssential = f
		return nil
	case "mce.threshold_headline":
		f, err := parseFloat(key, value, 0, 1)
		if err != nil {
			return err
		}
		s.MCEThresholdHeadline = f
		return nil
	case "mce.threshold_main":
		f, err := parseFloat(key, value, 0, 1)
		if err != nil {
			return err
		}
		s.MCEThresholdMain = f
		return nil
	case "mce.num_threads":
		n, err := parseInt(key, value, 1, 512)
		if err != nil {
			return err
		}
		s.MCENumThreads = ptr(int(n))
		return nil
	case "mce.chrome_post_process":
		return setBool(&s.MCEChromePostProcess, key, value)
	case "mce.exclude_regions":
		return setBool(&s.ExcludeRegions, key, value)
	case "mce.crop_main":
		b, err := parseBool(value)
		if err != nil {
			return fmt.Errorf("%s: %v", key, err)
		}
		s.CropMain = b
		return nil
	case "reader.format":
		f, err := NormalizeReaderFormat(value)
		if err != nil {
			return fmt.Errorf("%s: %v", key, err)
		}
		s.ReaderFormat = f
		return nil
	case "report.ocr_boxes":
		return setBool(&s.ReportOCRBoxes, key, value)
	case "report.mce_boxes":
		return setBool(&s.ReportMCEBoxes, key, value)
	case "tree.detect_headings":
		return setBool(&s.TreeDetectHeadings, key, value)
	case "tree.detect_lists":
		return setBool(&s.TreeDetectLists, key, value)
	case "tree.detect_links":
		return setBool(&s.TreeDetectLinks, key, value)
	case "tree.detect_regions":
		return setBool(&s.TreeDetectRegions, key, value)
	case "tree.heading_min_ratio":
		f, err := parseFloat(key, value, 1.05, 4)
		if err != nil {
			return err
		}
		s.TreeHeadingMinRatio = f
		return nil
	case "tree.paragraph_gap":
		f, err := parseFloat(key, value, 0, 4)
		if err != nil {
			return err
		}
		s.TreeParagraphGap = f
		return nil
	}
	return fmt.Errorf("setting %q is known but not implemented — this is a bug", key)
}

// ParseSetList applies every "key=value" entry in order.
func (s *Settings) ParseSetList(pairs []string) error {
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			return fmt.Errorf("--set expects key=value, got %q", p)
		}
		if err := s.Set(strings.TrimSpace(k), strings.TrimSpace(v)); err != nil {
			return err
		}
	}
	return nil
}

// Effective renders the currently-active (non-default) settings as
// "key=value" strings — used by `info`, result metadata and the server.
func (s *Settings) Effective() []string {
	if s == nil {
		return nil
	}
	var out []string
	add := func(format string, args ...any) {
		out = append(out, fmt.Sprintf(format, args...))
	}
	if s.OCRLightMode != nil {
		add("ocr.light_mode=%v", *s.OCRLightMode)
	}
	if s.DebugMode != nil {
		add("ocr.debug_mode=%v", *s.DebugMode)
	}
	if len(s.Scripts) > 0 {
		add("ocr.scripts=%s", strings.Join(s.Scripts, ","))
	}
	if len(s.Languages) > 0 {
		add("ocr.languages=%s", strings.Join(s.Languages, ","))
	}
	if s.MinConfidence > 0 {
		add("ocr.min_confidence=%g", s.MinConfidence)
	}
	if s.MaxImageDim > 0 {
		add("ocr.max_image_dimension=%d", s.MaxImageDim)
	}
	if s.TFLitePoolSize != nil {
		add("ocr.tflite_interpreter_pool_min_size=%d", *s.TFLitePoolSize)
	}
	if s.MaxInFlight != nil {
		add("ocr.max_in_flight=%d", *s.MaxInFlight)
	}
	if s.DetectionScoreThreshold != nil {
		add("ocr.detection_score_threshold=%g", *s.DetectionScoreThreshold)
	}
	if s.MCEExplicit {
		add("mce.enabled=%v", s.MCEEnabled)
	}
	if s.MCEThresholdNonEssential != nil {
		add("mce.threshold_non_essential=%g", *s.MCEThresholdNonEssential)
	}
	if s.MCEThresholdHeadline != nil {
		add("mce.threshold_headline=%g", *s.MCEThresholdHeadline)
	}
	if s.MCEThresholdMain != nil {
		add("mce.threshold_main=%g", *s.MCEThresholdMain)
	}
	if s.MCENumThreads != nil {
		add("mce.num_threads=%d", *s.MCENumThreads)
	}
	if s.MCEChromePostProcess != nil {
		add("mce.chrome_post_process=%v", *s.MCEChromePostProcess)
	}
	if s.ExcludeRegions != nil {
		add("mce.exclude_regions=%v", *s.ExcludeRegions)
	}
	if s.CropMain {
		add("mce.crop_main=true")
	}
	if f, err := NormalizeReaderFormat(s.ReaderFormat); err == nil && f != "" && f != "none" {
		add("reader.format=%s", f)
	}
	if s.ReportOCRBoxes != nil {
		add("report.ocr_boxes=%v", *s.ReportOCRBoxes)
	}
	if s.ReportMCEBoxes != nil {
		add("report.mce_boxes=%v", *s.ReportMCEBoxes)
	}
	if s.TreeDetectHeadings != nil {
		add("tree.detect_headings=%v", *s.TreeDetectHeadings)
	}
	if s.TreeDetectLists != nil {
		add("tree.detect_lists=%v", *s.TreeDetectLists)
	}
	if s.TreeDetectLinks != nil {
		add("tree.detect_links=%v", *s.TreeDetectLinks)
	}
	if s.TreeDetectRegions != nil {
		add("tree.detect_regions=%v", *s.TreeDetectRegions)
	}
	if s.TreeHeadingMinRatio != nil {
		add("tree.heading_min_ratio=%g", *s.TreeHeadingMinRatio)
	}
	if s.TreeParagraphGap != nil {
		add("tree.paragraph_gap=%g", *s.TreeParagraphGap)
	}
	return out
}

// TreeOptions resolves the tree.* settings into the pagetree
// construction options, applying stock defaults for unset fields.
func (s *Settings) TreeOptions() pagetree.TreeOptions {
	if s == nil {
		return pagetree.DefaultTreeOptions()
	}
	opts := pagetree.DefaultTreeOptions()
	if s.TreeDetectHeadings != nil {
		opts.DetectHeadings = *s.TreeDetectHeadings
	}
	if s.TreeDetectLists != nil {
		opts.DetectLists = *s.TreeDetectLists
	}
	if s.TreeDetectLinks != nil {
		opts.DetectLinks = *s.TreeDetectLinks
	}
	if s.TreeDetectRegions != nil {
		opts.DetectRegions = *s.TreeDetectRegions
	}
	if s.TreeHeadingMinRatio != nil {
		opts.HeadingMinRatio = *s.TreeHeadingMinRatio
	}
	if s.TreeParagraphGap != nil {
		opts.ParagraphGap = *s.TreeParagraphGap
	}
	return opts
}

// --- value parsing helpers ---

// NormalizeReaderFormat maps the accepted spellings of the reader
// export format to the canonical value. "" and "none" mean off (an
// empty canonical value is returned for "").
func NormalizeReaderFormat(v string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "":
		return "", nil
	case "none", "off":
		return "none", nil
	case "text", "txt":
		return "text", nil
	case "md", "markdown":
		return "markdown", nil
	case "html", "htm":
		return "html", nil
	}
	return "", fmt.Errorf("want text|markdown|html|none, got %q", v)
}

func setBool(dst **bool, key, value string) error {
	b, err := parseBool(value)
	if err != nil {
		return fmt.Errorf("%s: %v", key, err)
	}
	*dst = ptr(b)
	return nil
}

func parseBool(v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("want true/false, got %q", v)
}

func parseInt(key, v string, min, max int64) (int64, error) {
	var n int64
	if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &n); err != nil {
		return 0, fmt.Errorf("%s: want an integer, got %q", key, v)
	}
	if n < min || n > max {
		return 0, fmt.Errorf("%s: %d out of range [%d, %d]", key, n, min, max)
	}
	return n, nil
}

func parseFloat(key, v string, min, max float64) (*float64, error) {
	var f float64
	if _, err := fmt.Sscanf(strings.TrimSpace(v), "%g", &f); err != nil {
		return nil, fmt.Errorf("%s: want a number, got %q", key, v)
	}
	if f < min || f > max {
		return nil, fmt.Errorf("%s: %g out of range [%g, %g]", key, f, min, max)
	}
	return &f, nil
}

func ptr[T any](v T) *T { return &v }

func levenshtein(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := 0; j <= len(b); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
