package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadJSONNestedAndFlat(t *testing.T) {
	nested := `{
                "_readme": "doc keys are ignored",
                "ocr": {"min_confidence": 0.5, "languages": ["en", "de"]},
                "mce": {"threshold_non_essential": 0.9, "crop_main": true},
                "reader": {"format": "text"}
        }`
	s := Default()
	if err := LoadJSONFile(writeTemp(t, nested), s); err != nil {
		t.Fatalf("nested: %v", err)
	}
	if s.MinConfidence != 0.5 {
		t.Errorf("MinConfidence = %v, want 0.5", s.MinConfidence)
	}
	// ParseLanguages normalizes and sorts, so [en de] arrives as [de en]
	if len(s.Languages) != 2 || s.Languages[0] != "de" || s.Languages[1] != "en" {
		t.Errorf("Languages = %v, want [de en] (normalized)", s.Languages)
	}
	if s.MCEThresholdNonEssential == nil || *s.MCEThresholdNonEssential != 0.9 {
		t.Errorf("MCEThresholdNonEssential not applied")
	}
	if !s.CropMain {
		t.Errorf("CropMain not applied")
	}
	if s.ReaderFormat != "text" {
		t.Errorf("ReaderFormat = %q, want text", s.ReaderFormat)
	}
}

func TestLoadJSONDocKeysIgnored(t *testing.T) {
	doc := `{
                "_readme": ["docs", "ignored"],
                "ocr": {"_doc": "engine knobs", "light_mode": true}
        }`
	s := Default()
	if err := LoadJSONFile(writeTemp(t, doc), s); err != nil {
		t.Fatalf("doc keys: %v", err)
	}
	if s.OCRLightMode == nil || !*s.OCRLightMode {
		t.Errorf("light_mode not applied next to _doc")
	}
}

func TestLoadJSONEmptyMeansUnset(t *testing.T) {
	doc := `{"ocr": {"scripts": [], "languages": ""}}`
	s := Default()
	if err := LoadJSONFile(writeTemp(t, doc), s); err != nil {
		t.Fatalf("empty values: %v", err)
	}
	if len(s.Scripts) != 0 || len(s.Languages) != 0 {
		t.Errorf("empty values should leave lists unset")
	}
}

func TestLoadJSONUnknownKeyFails(t *testing.T) {
	for _, doc := range []string{
		`{"mce.threshold_typo": 1}`,
		`{"mce": {"threshold_typo": 1}}`,
		`{"unknown_group": {"x": 1}}`,
	} {
		s := Default()
		if err := LoadJSONFile(writeTemp(t, doc), s); err == nil {
			t.Errorf("unknown key did not fail: %s", doc)
		}
	}
}

func TestTemplateRoundTrip(t *testing.T) {
	s := Default()
	if err := LoadJSONFile(writeTemp(t, Template()), s); err != nil {
		t.Fatalf("template does not load: %v", err)
	}
	if !s.MCEEnabled {
		t.Errorf("template must keep mce.enabled true")
	}
	if s.CropMain {
		t.Errorf("template must keep crop_main false")
	}
	if f, _ := NormalizeReaderFormat(s.ReaderFormat); f != "none" {
		t.Errorf("template reader.format = %q, want none", s.ReaderFormat)
	}
}

func TestDiscover(t *testing.T) {
	t.Setenv("SCREENAI_SETTINGS", "")
	if path, ok := Discover(); ok {
		t.Fatalf("Discover in empty temp dir found %q", path)
	}
	path := writeTemp(t, `{"reader": {"format": "markdown"}}`)
	t.Setenv("SCREENAI_SETTINGS", path)
	got, ok := Discover()
	if !ok || got != path {
		t.Fatalf("Discover = %q, %v; want %q, true", got, ok, path)
	}
	s := Default()
	if _, err := LoadAuto(s); err != nil {
		t.Fatalf("LoadAuto: %v", err)
	}
	if s.ReaderFormat != "markdown" {
		t.Errorf("LoadAuto did not apply reader.format")
	}
}

func TestNormalizeReaderFormat(t *testing.T) {
	cases := map[string]string{
		"": "none-off", "none": "none", "off": "none",
		"text": "text", "txt": "txt", "md": "md", "markdown": "markdown",
		"html": "html", "htm": "htm", "Markdown": "Markdown",
	}
	for in := range cases {
		if _, err := NormalizeReaderFormat(in); err != nil {
			t.Errorf("NormalizeReaderFormat(%q) errored: %v", in, err)
		}
	}
	if _, err := NormalizeReaderFormat("pdf"); err == nil {
		t.Errorf("pdf should be rejected")
	}
}
