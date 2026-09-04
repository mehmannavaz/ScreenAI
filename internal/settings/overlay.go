package settings

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/mehmannavaz/ScreenAI/internal/pb"
)

// Base file names this package knows how to rewrite (the engine's file
// cache matches by base name because the library requests files by
// slash-varying relative paths; full reference paths in comments).
// Verified live against the component:
//
//	gocr_mobile_chrome_multiscript_2024_q4_engine.binarypb   (engine graph)
//	gocr/gocr_models/gocr_line_recognition_omni_mobile_chrome_multiscript_2024_q4.binarypb
//	gocr/gocr_models/detection/gocr_group_rpn_text_detection_config_2024_q4_chrome.binarypb
//	screen2x_config.pbtxt                                     (Screen2x)
const (
	engineConfigBase    = "gocr_mobile_chrome_multiscript_2024_q4_engine.binarypb"
	registryConfigBase  = "gocr_line_recognition_omni_mobile_chrome_multiscript_2024_q4.binarypb"
	detectionConfigBase = "gocr_group_rpn_text_detection_config_2024_q4_chrome.binarypb"
	screen2xConfigBase  = "screen2x_config.pbtxt"
)

// PatchedFiles lists the VFS paths this package knows how to rewrite.
var PatchedFiles = []string{
	engineConfigBase,
	"gocr/gocr_models/" + registryConfigBase,
	"gocr/gocr_models/detection/" + detectionConfigBase,
	screen2xConfigBase,
}

// PatchesFile reports whether the settings touch the given VFS path
// (slash-normalized or base name).
func (s *Settings) PatchesFile(rel string) bool {
	if s == nil {
		return false
	}
	base := rel
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		base = rel[i+1:]
	}
	switch base {
	case engineConfigBase:
		return s.TFLitePoolSize != nil || s.MaxInFlight != nil
	case registryConfigBase:
		return len(s.Scripts) > 0
	case detectionConfigBase:
		return s.DetectionScoreThreshold != nil
	case screen2xConfigBase:
		return s.MCEThresholdNonEssential != nil ||
			s.MCEThresholdHeadline != nil ||
			s.MCEThresholdMain != nil ||
			s.MCENumThreads != nil ||
			s.MCEChromePostProcess != nil
	}
	return false
}

// Apply rewrites one virtual file according to the active settings and
// returns the new bytes. rel is the library-requested relative path
// (slash-normalized, no leading slash). Files that match no patcher —
// or whose patcher finds nothing to change — pass through unchanged.
//
// A structurally unexpected file is an error: it means Google changed
// the config layout in a newer component and the patcher must be
// updated, so callers should surface it loudly instead of silently
// ignoring the requested setting.
func (s *Settings) Apply(rel string, data []byte) ([]byte, error) {
	if s == nil || len(data) == 0 {
		return data, nil
	}
	base := rel
	if i := strings.LastIndexByte(rel, '/'); i >= 0 {
		base = rel[i+1:]
	}
	switch base {
	case engineConfigBase:
		return s.patchEngineConfig(data)
	case registryConfigBase:
		return s.patchScriptRegistry(data)
	case detectionConfigBase:
		return s.patchDetectionConfig(data)
	case screen2xConfigBase:
		return s.patchScreen2xPbtxt(data)
	}
	return data, nil
}

// ---------------------------------------------------------------------------
// screen2x_config.pbtxt (text proto)
// ---------------------------------------------------------------------------

// patchScreen2xPbtxt rewrites the Screen2x class thresholds, thread
// count and post-processing flag inside the text-proto config. The
// patcher is line-oriented and shape-tolerant: it only rewrites values
// it fully recognizes, leaving everything else byte-identical.
func (s *Settings) patchScreen2xPbtxt(data []byte) ([]byte, error) {
	text := string(data)
	changed := false

	// The three class_thresholds blocks are patched by their label
	// line, not by position, so future reorderings still work. A
	// prediction_threshold line belongs to the block whose label is the
	// nearest previous non-empty line.
	patchThreshold := func(label string, value *float64) {
		if value == nil {
			return
		}
		lines := strings.Split(text, "\n")
		for i, line := range lines {
			if !strings.HasPrefix(strings.TrimSpace(line), "prediction_threshold:") {
				continue
			}
			for j := i - 1; j >= 0; j-- {
				prev := strings.TrimSpace(lines[j])
				if prev == "" {
					continue
				}
				if strings.HasPrefix(prev, "label: "+label) {
					lines[i] = replaceAfterColon(line, trimFloat(*value))
					changed = true
				}
				break
			}
		}
		text = strings.Join(lines, "\n")
	}
	patchThreshold("CONTENT_NON_ESSENTIAL", s.MCEThresholdNonEssential)
	patchThreshold("CONTENT_TEXT_HEADLINE", s.MCEThresholdHeadline)
	patchThreshold("CONTENT_TEXT_MAIN", s.MCEThresholdMain)

	if s.MCENumThreads != nil {
		if old, ok := replaceKeyLine(text, "num_threads:", trimInt(*s.MCENumThreads)); ok {
			text, changed = old, true
		} else {
			return nil, fmt.Errorf("screen2x config: no num_threads line found")
		}
	}
	if s.MCEChromePostProcess != nil {
		if old, ok := replaceKeyLine(text, "chrome_post_process:", boolStr(*s.MCEChromePostProcess)); ok {
			text, changed = old, true
		} else {
			return nil, fmt.Errorf("screen2x config: no chrome_post_process line found")
		}
	}
	if !changed {
		return data, nil
	}
	return []byte(text), nil
}

func replaceAfterColon(line, value string) string {
	i := strings.Index(line, ":")
	if i < 0 {
		return line
	}
	return line[:i+1] + " " + value
}

func replaceKeyLine(text, key, value string) (string, bool) {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), key) {
			lines[i] = replaceAfterColon(lines[i], value)
			return strings.Join(lines, "\n"), true
		}
	}
	return text, false
}

func trimFloat(v float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", v), "0"), ".")
}

func trimInt(v int) string { return fmt.Sprintf("%d", v) }

func boolStr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// ---------------------------------------------------------------------------
// OCR engine graph (binary protobuf, template parameter defaults)
// ---------------------------------------------------------------------------

// patchEngineConfig rewrites the template parameter defaults inside
// the MediaPipe OCR graph. The binding structure (recovered from the
// shipped binarypb) is:
//
//	(top) { field 2 repeated: binding {
//	            2: "param",          // expression token
//	            3: { 1: <name> },    // parameter name
//	            3: { 1: <value>, 2: "literal" },   // default value
//	        } }
//
// Only the two parameters verified safe against the real binary are
// rewritten; the structural booleans (use_aksara_layout, ...) are left
// alone because the component does not ship the sub-models they gate.
func (s *Settings) patchEngineConfig(data []byte) ([]byte, error) {
	overrides := map[string]string{}
	if s.TFLitePoolSize != nil {
		overrides["tflite_interpreter_pool_min_size"] = trimInt(*s.TFLitePoolSize)
	}
	if s.MaxInFlight != nil {
		overrides["max_in_flight"] = trimInt(*s.MaxInFlight)
	}
	if len(overrides) == 0 {
		return data, nil
	}

	var fields []pb.Field
	if err := pb.ForEachField(data, func(f pb.Field) bool {
		fields = append(fields, f)
		return true
	}); err != nil {
		return nil, fmt.Errorf("engine config: %v", err)
	}

	out := make([]byte, 0, len(data))
	changed := map[string]bool{}
	for _, f := range fields {
		if f.Num != 2 || f.Wire != pb.WireBytes {
			out = appendField(out, f)
			continue
		}
		patched, name, err := patchParamBinding(f.Bytes, overrides)
		if err != nil {
			return nil, fmt.Errorf("engine config: %v", err)
		}
		if name != "" {
			changed[name] = true
		}
		out = pb.AppendBytesField(out, 2, patched)
	}
	for name := range overrides {
		if !changed[name] {
			return nil, fmt.Errorf("engine config: parameter %q not found (component layout changed?)", name)
		}
	}
	return out, nil
}

// patchParamBinding rewrites one field-2 binding message when it
// matches a requested override. Returns the new payload, the parameter
// name it patched ("" when untouched).
func patchParamBinding(binding []byte, overrides map[string]string) ([]byte, string, error) {
	var fields []pb.Field
	if err := pb.ForEachField(binding, func(f pb.Field) bool {
		fields = append(fields, f)
		return true
	}); err != nil {
		return nil, "", err
	}

	var name string
	for i, f := range fields {
		if f.Num == 2 && f.Wire == pb.WireBytes && string(f.Bytes) == "param" {
			// the parameter name is the first field-3 message after the
			// "param" token
			for j := i + 1; j < len(fields); j++ {
				if fields[j].Num == 3 && fields[j].Wire == pb.WireBytes {
					name = firstStringField(fields[j].Bytes)
					break
				}
			}
		}
	}
	if name == "" || overrides[name] == "" {
		return binding, "", nil
	}

	// Rebuild: keep every field identical except the value message
	// (a field-3 whose payload is {1: <literal>, 2: "literal"}).
	newValue := appendLiteralValue(nil, overrides[name])
	out := make([]byte, 0, len(binding))
	replaced := false
	nameDone := false
	for _, f := range fields {
		if f.Num == 3 && f.Wire == pb.WireBytes {
			if !nameDone {
				// first field-3 is the parameter name — keep as is
				nameDone = true
				out = pb.AppendBytesField(out, f.Num, f.Bytes)
				continue
			}
			if !replaced && isLiteralValue(f.Bytes) {
				out = pb.AppendBytesField(out, f.Num, newValue)
				replaced = true
				continue
			}
		}
		out = appendField(out, f)
	}
	if !replaced {
		return nil, "", fmt.Errorf("parameter %q has no literal default to replace", name)
	}
	return out, name, nil
}

// firstStringField returns field 1 of a message when it is a string.
func firstStringField(msg []byte) string {
	var out string
	_ = pb.ForEachField(msg, func(f pb.Field) bool {
		if f.Num == 1 && f.Wire == pb.WireBytes {
			out = string(f.Bytes)
			return false
		}
		return true
	})
	return out
}

// isLiteralValue reports whether msg looks like {1: <value>, 2: "literal"}.
func isLiteralValue(msg []byte) bool {
	hasLit, hasVal := false, false
	err := pb.ForEachField(msg, func(f pb.Field) bool {
		switch {
		case f.Num == 1 && f.Wire == pb.WireBytes:
			hasVal = true
		case f.Num == 2 && f.Wire == pb.WireBytes && string(f.Bytes) == "literal":
			hasLit = true
		}
		return true
	})
	return err == nil && hasLit && hasVal
}

// appendLiteralValue builds {1: <value>, 2: "literal"}.
func appendLiteralValue(dst []byte, value string) []byte {
	dst = pb.AppendStringField(dst, 1, value)
	return pb.AppendStringField(dst, 2, "literal")
}

func appendField(dst []byte, f pb.Field) []byte {
	switch f.Wire {
	case pb.WireVarint:
		return pb.AppendVarintField(dst, f.Num, f.Varint)
	case pb.WireFixed64:
		dst = pb.AppendVarint(dst, uint64(f.Num)<<3|uint64(pb.WireFixed64))
		var scratch [8]byte
		v := f.Fixed64
		scratch[0], scratch[1], scratch[2], scratch[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
		scratch[4], scratch[5], scratch[6], scratch[7] = byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56)
		return append(dst, scratch[:]...)
	case pb.WireFixed32:
		dst = pb.AppendVarint(dst, uint64(f.Num)<<3|uint64(pb.WireFixed32))
		var scratch [4]byte
		v := f.Fixed32
		scratch[0], scratch[1], scratch[2], scratch[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
		return append(dst, scratch[:]...)
	default:
		return pb.AppendBytesField(dst, f.Num, f.Bytes)
	}
}

// ---------------------------------------------------------------------------
// Script registry (binary protobuf, Any-wrapped)
// ---------------------------------------------------------------------------

// patchScriptRegistry filters the script → recognizer map. Structure:
//
//	(top) { 1: type_url, 2: config {
//	    1 repeated: { 1: <script>, 2: { 2: <config path>, ... } },
//	    2: selector { 1: "LanguageBasedLineRecognizerConfigSelector",
//	                  3: "und" },
//	    4: 60000, 5: 2, 6: 1, 7: 1,
//	} }
//
// 'und' is the universal/Latin fallback the selector points at, so it
// is always preserved.
func (s *Settings) patchScriptRegistry(data []byte) ([]byte, error) {
	if len(s.Scripts) == 0 {
		return data, nil
	}
	keep := map[string]bool{"und": true}
	for _, sc := range s.Scripts {
		if scripts := langToScripts[sc]; len(scripts) > 0 {
			for _, x := range scripts {
				keep[x] = true
			}
		} else {
			keep[sc] = true
		}
	}

	var fields []pb.Field
	if err := pb.ForEachField(data, func(f pb.Field) bool {
		fields = append(fields, f)
		return true
	}); err != nil {
		return nil, fmt.Errorf("script registry: %v", err)
	}

	out := make([]byte, 0, len(data))
	sawScript, keptScripts := 0, 0
	for _, f := range fields {
		if f.Num == 2 && f.Wire == pb.WireBytes {
			cfg, n, k, err := filterRegistryConfig(f.Bytes, keep)
			if err != nil {
				return nil, fmt.Errorf("script registry: %v", err)
			}
			sawScript, keptScripts = sawScript+n, keptScripts+k
			out = pb.AppendBytesField(out, 2, cfg)
			continue
		}
		out = appendField(out, f)
	}
	if sawScript == 0 {
		return nil, fmt.Errorf("script registry: no script entries found (component layout changed?)")
	}
	return out, nil
}

func filterRegistryConfig(cfg []byte, keep map[string]bool) (out []byte, saw, kept int, err error) {
	var fields []pb.Field
	if err = pb.ForEachField(cfg, func(f pb.Field) bool {
		fields = append(fields, f)
		return true
	}); err != nil {
		return nil, 0, 0, err
	}
	out = make([]byte, 0, len(cfg))
	for _, f := range fields {
		if f.Num == 1 && f.Wire == pb.WireBytes {
			saw++
			script := firstStringField(f.Bytes)
			if keep[script] {
				kept++
				out = pb.AppendBytesField(out, 1, f.Bytes)
			}
			continue
		}
		out = appendField(out, f)
	}
	return out, saw, kept, nil
}

// langToScripts maps language codes to the registry script entries
// they need. Latin languages deliberately map to nothing extra: they
// ride on the always-kept 'und' universal recognizer.
var langToScripts = map[string][]string{
	// Arabic
	"ar": {"Arab"},
	// Armenian
	"hy": {"Armn"}, // n/a in registry; falls back to und via selector
	// Bengali
	"bn": {"Beng"},
	// Cyrillic
	"ru": {"Cyrl"}, "bg": {"Cyrl"}, "uk": {"Cyrl"}, "be": {"Cyrl"},
	"sr": {"Cyrl"}, "mk": {"Cyrl"}, "kk": {"Cyrl"}, "ky": {"Cyrl"},
	"tg": {"Cyrl"}, "mn": {"Cyrl"}, "uz": {"Cyrl"}, "az": {"Cyrl"},
	// Devanagari
	"hi": {"Deva"}, "mr": {"Deva"}, "ne": {"Deva"}, "sa": {"Deva"},
	// Georgian
	"ka": {"Geor"},
	// Greek
	"el": {"Grek"},
	// Gujarati
	"gu": {"Gujr"},
	// Gurmukhi
	"pa": {"Guru"},
	// Han / CJK
	"zh": {"Hani"}, "ja": {"Jpan", "Hani"}, "ko": {"Kore", "Hani"},
	// Hebrew
	"he": {"Hebr"}, "yi": {"Hebr"},
	// Khmer
	"km": {"Khmr"},
	// Kannada
	"kn": {"Knda"},
	// Lao
	"lo": {"Laoo"},
	// Malayalam
	"ml": {"Mlym"},
	// Sinhala
	"si": {"Sinh"},
	// Tamil
	"ta": {"Taml"},
	// Telugu
	"te": {"Telu"},
	// Thai
	"th": {"Thai"},
	// Latin languages ride on 'und' (always kept) — no explicit entry.
	"en": {}, "fr": {}, "de": {}, "es": {}, "it": {}, "pt": {},
	"nl": {}, "da": {}, "sv": {}, "no": {}, "fi": {}, "is": {},
	"pl": {}, "cs": {}, "sk": {}, "hu": {}, "ro": {}, "lt": {},
	"lv": {}, "et": {}, "sl": {}, "hr": {}, "sq": {}, "tr": {},
	"id": {}, "ms": {}, "vi": {}, "sw": {}, "af": {}, "ca": {},
	"fil": {}, "tl": {}, "eu": {}, "gl": {}, "cy": {}, "ga": {},
}

// ParseScripts normalizes a user script/language list: script codes
// pass through, language codes map through langToScripts, and 'und' is
// always included.
func ParseScripts(values []string) ([]string, error) {
	seen := map[string]bool{"und": true}
	var out []string
	for _, raw := range values {
		v := normalizeCode(raw)
		if v == "" {
			continue
		}
		if strings.ToLower(v) == "all" {
			return nil, nil // explicit all = no restriction
		}
		mapped, ok := langToScripts[v]
		if !ok {
			// treat as a raw script code
			mapped = []string{v}
		}
		for _, sc := range mapped {
			if sc == "" || seen[sc] {
				continue
			}
			seen[sc] = true
			out = append(out, sc)
		}
	}
	sort.Strings(out)
	return out, nil
}

// ParseLanguages normalizes a user language list for the post-filter.
func ParseLanguages(values []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, raw := range values {
		v := normalizeCode(raw)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out, nil
}

func normalizeCode(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	return strings.TrimPrefix(s, "script:")
}

// ScriptCodes returns the ISO 15924 script codes the registry restricts
// to for the current Scripts setting (nil = unrestricted).
func (s *Settings) ScriptCodes() []string {
	if s == nil || len(s.Scripts) == 0 {
		return nil
	}
	codes, _ := ParseScripts(s.Scripts)
	return codes
}

// ---------------------------------------------------------------------------
// Text detection config (binary protobuf, Any-wrapped)
// ---------------------------------------------------------------------------

// patchDetectionConfig rewrites the detection sensitivity float at
// top → {2: config} → {2: post-processing} → field 1 (fixed32).
// Verified live: 0.5 → 0.9 changes the number of detected text lines.
func (s *Settings) patchDetectionConfig(data []byte) ([]byte, error) {
	if s.DetectionScoreThreshold == nil {
		return data, nil
	}
	score := float32(*s.DetectionScoreThreshold)

	var fields []pb.Field
	if err := pb.ForEachField(data, func(f pb.Field) bool {
		fields = append(fields, f)
		return true
	}); err != nil {
		return nil, fmt.Errorf("detection config: %v", err)
	}

	out := make([]byte, 0, len(data))
	patched := false
	for _, f := range fields {
		if f.Num == 2 && f.Wire == pb.WireBytes {
			cfg, err := patchDetectionLevel2(f.Bytes, score, &patched)
			if err != nil {
				return nil, fmt.Errorf("detection config: %v", err)
			}
			out = pb.AppendBytesField(out, 2, cfg)
			continue
		}
		out = appendField(out, f)
	}
	if !patched {
		return nil, fmt.Errorf("detection config: score threshold field not found (component layout changed?)")
	}
	return out, nil
}

func patchDetectionLevel2(cfg []byte, score float32, patched *bool) ([]byte, error) {
	var fields []pb.Field
	if err := pb.ForEachField(cfg, func(f pb.Field) bool {
		fields = append(fields, f)
		return true
	}); err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(cfg))
	for _, f := range fields {
		if f.Num == 2 && f.Wire == pb.WireBytes && !*patched {
			inner, err := patchDetectionLevel3(f.Bytes, score, patched)
			if err != nil {
				return nil, err
			}
			out = pb.AppendBytesField(out, 2, inner)
			continue
		}
		out = appendField(out, f)
	}
	return out, nil
}

func patchDetectionLevel3(msg []byte, score float32, patched *bool) ([]byte, error) {
	var fields []pb.Field
	if err := pb.ForEachField(msg, func(f pb.Field) bool {
		fields = append(fields, f)
		return true
	}); err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(msg))
	for _, f := range fields {
		if f.Num == 1 && f.Wire == pb.WireFixed32 && !*patched {
			bits := math.Float32bits(score)
			f.Fixed32 = bits
			*patched = true
		}
		out = appendField(out, f)
	}
	return out, nil
}
