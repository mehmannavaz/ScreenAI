package settings

import (
	"fmt"
	"os"
)

// Discover returns the settings file that is auto-loaded at startup,
// checking in precedence order:
//
//	$SCREENAI_SETTINGS   explicit path (must exist)
//	./settings.json      conventional location
//	./screenai.settings.json
//
// The second return value reports whether a file was found. A file
// that exists but fails to parse is an error the caller should show,
// not silently skip — a typo'd settings.json must never run the
// engine with half the intended overrides.
//
// The auto-loaded file sits at the LOWEST precedence: an explicit
// --settings FILE and --set key=value pairs override it.
func Discover() (string, bool) {
	for _, path := range DiscoverPaths() {
		if path == "" {
			continue
		}
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, true
		}
	}
	return "", false
}

// LoadAuto loads the discovered settings file into s and returns the
// path it came from ("" when nothing was discovered). Values already
// set in s (there should be none — call this first) would be
// overwritten by later --set pairs anyway.
func LoadAuto(s *Settings) (string, error) {
	path, ok := Discover()
	if !ok {
		return "", nil
	}
	if err := LoadJSONFile(path, s); err != nil {
		return path, fmt.Errorf("auto settings: %v", err)
	}
	return path, nil
}
