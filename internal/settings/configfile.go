package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// LoadJSONFile reads a settings file. The format is a flat JSON object
// mapping settings keys to values; values may be strings, numbers,
// booleans or arrays of strings:
//
//	{
//	  "mce.threshold_non_essential": 0.99,
//	  "ocr.scripts": "und,Jpan,Kore,Hani",
//	  "ocr.languages": ["en", "de"]
//	}
//
// Keys are applied in sorted order (a file must not depend on order
// anyway) and unknown keys fail loudly, exactly like --set.
func LoadJSONFile(path string, into *Settings) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("settings file: %v", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("settings file %s: %v", path, err)
	}
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		value, err := jsonToValue(doc[k])
		if err != nil {
			return fmt.Errorf("settings file %s: %s: %v", path, k, err)
		}
		if err := into.Set(k, value); err != nil {
			return fmt.Errorf("settings file %s: %v", path, err)
		}
	}
	return nil
}

func jsonToValue(raw json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	switch {
	case trimmed == "null":
		return "", fmt.Errorf("null is not a settings value")
	case strings.HasPrefix(trimmed, "\""):
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", err
		}
		return s, nil
	case strings.HasPrefix(trimmed, "["):
		var arr []string
		if err := json.Unmarshal(raw, &arr); err != nil {
			// try numbers inside the array
			var nums []json.Number
			if err2 := json.Unmarshal(raw, &nums); err2 != nil {
				return "", err
			}
			parts := make([]string, len(nums))
			for i, n := range nums {
				parts[i] = n.String()
			}
			return strings.Join(parts, ","), nil
		}
		return strings.Join(arr, ","), nil
	default:
		// booleans and numbers pass through as their literal text
		return trimmed, nil
	}
}

// Reference renders the full settings table for `screenai settings`
// and the README.
func Reference() string {
	var b strings.Builder
	b.WriteString("Screen AI settings reference\n\n")
	b.WriteString("Apply with --set key=value (repeatable), --langs, --min-conf,\n")
	b.WriteString("or a JSON file via --settings FILE. See `screenai settings`.\n\n")
	for _, group := range []struct{ title, prefix string }{
		{"OCR (Google's closed-source gocr engine)", "ocr."},
		{"MCE (Screen2x main-content model)", "mce."},
	} {
		b.WriteString(group.title + "\n")
		for _, d := range Descriptors {
			if !strings.HasPrefix(d.Key, group.prefix) {
				continue
			}
			fmt.Fprintf(&b, "  %-38s %-6s default %-8s [%s]\n", d.Key, d.Type, d.Default, d.Mechanism)
			fmt.Fprintf(&b, "    %s\n", d.Summary)
			fmt.Fprintf(&b, "    %s\n", d.Effect)
		}
		b.WriteString("\n")
	}
	b.WriteString("Script codes for ocr.scripts: Arab Beng Cyrl Deva Geor Grek Gujr Guru\n")
	b.WriteString("Hani Hebr Jpan Khmr Knda Kore Laoo Mlym Sinh Taml Telu Thai und.\n")
	b.WriteString("Language codes (en, ru, ja, ...) are accepted too and mapped to\n")
	b.WriteString("scripts; Latin languages ride on the 'und' universal recognizer.\n")
	return b.String()
}
