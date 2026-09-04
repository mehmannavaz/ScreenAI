package settings

import (
	"math"
	"strings"
	"testing"

	"github.com/mehmannavaz/ScreenAI/internal/pb"
)

// ---------------------------------------------------------------------------
// screen2x_config.pbtxt patcher
// ---------------------------------------------------------------------------

const stockScreen2x = `# proto-file: knowledge/.../screen2x.proto
# proto-message: screenai.screen2x.Screen2xOptions

model_path: "knowledge/.../model.tflite"
model_config {
  mpnn_config {
    signature: "serving_default"
    inference_config {
      num_threads: 1
      class_thresholds {
        label: CONTENT_NON_ESSENTIAL
        prediction_threshold: 0.65
      }
      class_thresholds {
        label: CONTENT_TEXT_HEADLINE
        prediction_threshold: 0.0
      }
      class_thresholds {
        label: CONTENT_TEXT_MAIN
        prediction_threshold: 0.0
      }
    }
  }
}
post_process_options {
  chrome_post_process: true
}
`

func TestPatchScreen2xPbtxtThresholds(t *testing.T) {
	s := Default()
	if err := s.Set("mce.threshold_non_essential", "0.01"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("mce.threshold_headline", "0.5"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("mce.threshold_main", "0.9"); err != nil {
		t.Fatal(err)
	}
	out, err := s.Apply("screen2x_config.pbtxt", []byte(stockScreen2x))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{
		"label: CONTENT_NON_ESSENTIAL\n        prediction_threshold: 0.01",
		"label: CONTENT_TEXT_HEADLINE\n        prediction_threshold: 0.5",
		"label: CONTENT_TEXT_MAIN\n        prediction_threshold: 0.9",
		"num_threads: 1",
		"chrome_post_process: true",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("patched config missing %q\n got:\n%s", want, got)
		}
	}
	// nothing else may change
	if strings.Count(got, "class_thresholds") != 3 {
		t.Errorf("block count changed: %d", strings.Count(got, "class_thresholds"))
	}
}

func TestPatchScreen2xPbtxtThreadsAndPostProcess(t *testing.T) {
	s := Default()
	if err := s.Set("mce.num_threads", "4"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("mce.chrome_post_process", "false"); err != nil {
		t.Fatal(err)
	}
	out, err := s.Apply("screen2x_config.pbtxt", []byte(stockScreen2x))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "num_threads: 4") {
		t.Errorf("num_threads not patched:\n%s", got)
	}
	if !strings.Contains(got, "chrome_post_process: false") {
		t.Errorf("chrome_post_process not patched:\n%s", got)
	}
	if !strings.Contains(got, "prediction_threshold: 0.65") {
		t.Errorf("threshold should be untouched:\n%s", got)
	}
}

func TestPatchScreen2xPbtxtNoop(t *testing.T) {
	s := Default()
	out, err := s.Apply("screen2x_config.pbtxt", []byte(stockScreen2x))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != stockScreen2x {
		t.Error("no-op settings must leave the file byte-identical")
	}
}

// ---------------------------------------------------------------------------
// engine graph parameter patcher
// ---------------------------------------------------------------------------

// buildEngineConfig synthesizes the graph-binding layout:
//
//	{ 2: binding{ 2:"param", 3:{1:"<name>"}, 3:{1:"<value>",2:"literal"} } }
func buildEngineConfig(name, value string) []byte {
	binding := pb.AppendStringField(nil, 2, "param")
	binding = pb.AppendMessageField(binding, 3, pb.AppendStringField(nil, 1, name))
	valMsg := pb.AppendStringField(nil, 1, value)
	valMsg = pb.AppendStringField(valMsg, 2, "literal")
	binding = pb.AppendMessageField(binding, 3, valMsg)
	return pb.AppendMessageField(nil, 2, binding)
}

func TestPatchEngineConfigParams(t *testing.T) {
	data := buildEngineConfig("tflite_interpreter_pool_min_size", "4")
	// add a second, untouched binding
	data = append(data, buildEngineConfig("unrelated_param", "keep")...)

	s := Default()
	if err := s.Set("ocr.tflite_interpreter_pool_min_size", "16"); err != nil {
		t.Fatal(err)
	}
	out, err := s.Apply("gocr_mobile_chrome_multiscript_2024_q4_engine.binarypb", data)
	if err != nil {
		t.Fatal(err)
	}
	// decode and verify both bindings
	var poolOK, unrelatedOK bool
	if err := pb.ForEachField(out, func(f pb.Field) bool {
		if f.Num != 2 {
			return true
		}
		_ = pb.ForEachField(f.Bytes, func(g pb.Field) bool {
			if g.Num == 3 && g.Wire == pb.WireBytes {
				_ = pb.ForEachField(g.Bytes, func(h pb.Field) bool {
					if h.Num == 1 && h.Wire == pb.WireBytes {
						switch string(h.Bytes) {
						case "16":
							poolOK = true
						case "4":
							poolOK = false
						case "keep":
							unrelatedOK = true
						}
					}
					return true
				})
			}
			return true
		})
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if !poolOK {
		t.Error("pool size literal not rewritten to 16")
	}
	if !unrelatedOK {
		t.Error("unrelated binding was modified")
	}
}

func TestPatchEngineConfigMissingParam(t *testing.T) {
	s := Default()
	if err := s.Set("ocr.max_in_flight", "2"); err != nil {
		t.Fatal(err)
	}
	data := buildEngineConfig("some_other_param", "1")
	if _, err := s.Apply("gocr_mobile_chrome_multiscript_2024_q4_engine.binarypb", data); err == nil {
		t.Error("missing parameter must be reported as an error, not silently ignored")
	}
}

// ---------------------------------------------------------------------------
// script registry patcher
// ---------------------------------------------------------------------------

// buildRegistry synthesizes the registry layout:
//
//	{ 1: type_url, 2: config{
//	    1: {1:"<script>", 2: {2:"<path>"}},
//	    2: {1:"LanguageBasedLineRecognizerConfigSelector", 3:"und"},
//	    4: 60000 } }
func buildRegistry(scripts map[string]string) []byte {
	cfg := make([]byte, 0, 256)
	for script, path := range scripts {
		entry := pb.AppendStringField(nil, 1, script)
		inner := pb.AppendStringField(nil, 2, path)
		entry = pb.AppendMessageField(entry, 2, inner)
		cfg = pb.AppendMessageField(cfg, 1, entry)
	}
	sel := pb.AppendStringField(nil, 1, "LanguageBasedLineRecognizerConfigSelector")
	sel = pb.AppendStringField(sel, 3, "und")
	cfg = pb.AppendMessageField(cfg, 2, sel)
	cfg = pb.AppendVarintField(cfg, 4, 60000)

	out := pb.AppendStringField(nil, 1, "type.googleapis.com/google_ocr.MultiPassLineRecognitionMutatorConfig")
	return pb.AppendMessageField(out, 2, cfg)
}

func registryScripts(data []byte) map[string]bool {
	scripts := map[string]bool{}
	_ = pb.ForEachField(data, func(f pb.Field) bool {
		if f.Num != 2 {
			return true
		}
		_ = pb.ForEachField(f.Bytes, func(g pb.Field) bool {
			if g.Num != 1 || g.Wire != pb.WireBytes {
				return true
			}
			scripts[firstStringField(g.Bytes)] = true
			return true
		})
		return true
	})
	return scripts
}

func TestPatchScriptRegistryFilter(t *testing.T) {
	data := buildRegistry(map[string]string{
		"Arab": "a.pb", "Cyrl": "c.pb", "Grek": "g.pb",
		"Jpan": "j.pb", "Kore": "k.pb", "Hani": "h.pb", "und": "u.pb",
	})
	s := Default()
	if err := s.Set("ocr.scripts", "en,el,ja"); err != nil {
		t.Fatal(err)
	}
	out, err := s.Apply("gocr_line_recognition_omni_mobile_chrome_multiscript_2024_q4.binarypb", data)
	if err != nil {
		t.Fatal(err)
	}
	got := registryScripts(out)
	// en → und (Latin), el → Grek, ja → Jpan+Hani; und always kept
	for _, want := range []string{"und", "Grek", "Jpan", "Hani"} {
		if !got[want] {
			t.Errorf("script %s missing from %v", want, got)
		}
	}
	for _, banned := range []string{"Arab", "Cyrl", "Kore"} {
		if got[banned] {
			t.Errorf("script %s should be removed, got %v", banned, got)
		}
	}
}

func TestPatchScriptRegistryNoop(t *testing.T) {
	data := buildRegistry(map[string]string{"Arab": "a.pb", "und": "u.pb"})
	s := Default()
	out, err := s.Apply("gocr_line_recognition_omni_mobile_chrome_multiscript_2024_q4.binarypb", data)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(data) {
		t.Error("empty script list must not touch the registry")
	}
}

func TestParseScriptsLanguageMapping(t *testing.T) {
	got, err := ParseScripts([]string{"ja", "ZH"})
	if err != nil {
		t.Fatal(err)
	}
	// ja → Jpan+Hani; zh → Hani; both ride sorted+deduped
	if len(got) != 2 || got[0] != "Hani" || got[1] != "Jpan" {
		t.Errorf("ParseScripts(ja, zh) = %v, want [Hani Jpan]", got)
	}
	got, err = ParseScripts([]string{"ru", "ar"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "Arab" || got[1] != "Cyrl" {
		t.Errorf("ParseScripts(ru, ar) = %v, want [Arab Cyrl]", got)
	}
}

// ---------------------------------------------------------------------------
// detection config patcher
// ---------------------------------------------------------------------------

// buildDetectionConfig synthesizes: {1: type_url, 2: {2: {1: <float>}}}
func buildDetectionConfig(score float32) []byte {
	post := pb.AppendFloatField(nil, 1, float64(score))
	cfg := pb.AppendMessageField(nil, 2, post)
	out := pb.AppendStringField(nil, 1, "type.googleapis.com/google_ocr.GroupRpnTextDetectionMutatorConfig")
	return pb.AppendMessageField(out, 2, cfg)
}

func detectionScore(data []byte) float32 {
	var out float32
	_ = pb.ForEachField(data, func(f pb.Field) bool {
		if f.Num != 2 {
			return true
		}
		_ = pb.ForEachField(f.Bytes, func(g pb.Field) bool {
			if g.Num != 2 {
				return true
			}
			_ = pb.ForEachField(g.Bytes, func(h pb.Field) bool {
				if h.Num == 1 && h.Wire == pb.WireFixed32 {
					out = math.Float32frombits(h.Fixed32)
				}
				return true
			})
			return true
		})
		return true
	})
	return out
}

func TestPatchDetectionConfigScore(t *testing.T) {
	s := Default()
	if err := s.Set("ocr.detection_score_threshold", "0.9"); err != nil {
		t.Fatal(err)
	}
	out, err := s.Apply("gocr_group_rpn_text_detection_config_2024_q4_chrome.binarypb", buildDetectionConfig(0.5))
	if err != nil {
		t.Fatal(err)
	}
	if got := detectionScore(out); math.Abs(float64(got-0.9)) > 1e-6 {
		t.Errorf("score = %v, want 0.9", got)
	}
}

func TestPatchDetectionConfigNoop(t *testing.T) {
	s := Default()
	data := buildDetectionConfig(0.5)
	out, err := s.Apply("gocr_group_rpn_text_detection_config_2024_q4_chrome.binarypb", data)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(data) {
		t.Error("no detection setting must leave the config untouched")
	}
}

// ---------------------------------------------------------------------------
// key registry / parsing
// ---------------------------------------------------------------------------

func TestSetUnknownKey(t *testing.T) {
	s := Default()
	err := s.Set("mce.threshold_non_essental", "0.5") // typo
	if err == nil {
		t.Fatal("typo should fail")
	}
	if !strings.Contains(err.Error(), "did you mean") {
		t.Errorf("error should suggest: %v", err)
	}
}

func TestSetValidation(t *testing.T) {
	cases := []struct{ key, value string }{
		{"mce.threshold_non_essential", "1.5"}, // out of [0,1]
		{"mce.num_threads", "0"},               // min 1
		{"ocr.max_in_flight", "-1"},            // min 1
		{"ocr.min_confidence", "2"},            // out of [0,1]
		{"ocr.light_mode", "maybe"},            // not a bool
	}
	for _, c := range cases {
		s := Default()
		if err := s.Set(c.key, c.value); err == nil {
			t.Errorf("Set(%q, %q) should fail", c.key, c.value)
		}
	}
}

func TestEffective(t *testing.T) {
	s := Default()
	_ = s.Set("mce.threshold_non_essential", "0.99")
	_ = s.Set("ocr.scripts", "ja")
	_ = s.Set("ocr.languages", "en,fr")
	_ = s.Set("ocr.min_confidence", "0.8")
	eff := strings.Join(s.Effective(), " ")
	for _, want := range []string{
		"mce.threshold_non_essential=0.99",
		"ocr.scripts=Hani,Jpan",
		"ocr.languages=en,fr",
		"ocr.min_confidence=0.8",
	} {
		if !strings.Contains(eff, want) {
			t.Errorf("Effective() missing %q in %q", want, eff)
		}
	}
}

func TestPatchesFile(t *testing.T) {
	s := Default()
	if s.PatchesFile("screen2x_config.pbtxt") {
		t.Error("empty settings patch nothing")
	}
	_ = s.Set("mce.num_threads", "2")
	_ = s.Set("ocr.scripts", "ru")
	if !s.PatchesFile("screen2x_config.pbtxt") {
		t.Error("mce.num_threads should patch screen2x config")
	}
	if !s.PatchesFile("gocr/gocr_models/gocr_line_recognition_omni_mobile_chrome_multiscript_2024_q4.binarypb") {
		t.Error("scripts should patch the registry")
	}
	if s.PatchesFile("gocr_mobile_chrome_multiscript_2024_q4_engine.binarypb") {
		t.Error("engine graph should not be patched")
	}
}
