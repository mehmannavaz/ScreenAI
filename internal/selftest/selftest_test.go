package selftest

import "testing"

// TestRunWithoutComponent executes the hermetic codec checks (the engine
// smoke test is skipped when no component is present — as on CI).
func TestRunWithoutComponent(t *testing.T) {
	results := Run("")
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("%s: %v", r.Name, r.Err)
		}
	}
}
