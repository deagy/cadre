package selector

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestDumpCanonicalForParityProbe writes this encoder's output for a set of
// hostile values plus every recorded golden plan, so it can be diffed against
// Python's json.dumps. Not an assertion; see TestDumpMatchesForParityProbe.
func TestDumpCanonicalForParityProbe(t *testing.T) {
	destination := os.Getenv("CADRE_SELECTOR_CANONICAL_PROBE_OUT")
	if destination == "" {
		t.Skip("set CADRE_SELECTOR_CANONICAL_PROBE_OUT to dump canonical encodings")
	}
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	inputs := readJSONMap(t, filepath.Join(repoRoot, "roster", "orchestration", "test",
		"testdata", "canonical_cases.json"))

	results := map[string]any{}
	for name, value := range inputs {
		encoded, err := CanonicalJSON(value)
		if err != nil {
			t.Fatalf("CanonicalJSON(%s): %v", name, err)
		}
		results[name] = string(encoded)
	}

	// Every recorded golden plan, re-encoded. These are the real shapes the
	// fingerprint is computed over.
	goldens := readJSONMap(t, filepath.Join(repoRoot, "roster", "orchestration", "test",
		"select_golden.json"))
	for id, raw := range goldens {
		entry, _ := raw.(map[string]any)
		encoded, err := CanonicalJSON(entry["canonical"])
		if err != nil {
			t.Fatalf("CanonicalJSON(golden %s): %v", id, err)
		}
		results["golden|"+id] = string(encoded)
	}

	encoded, _ := json.MarshalIndent(results, "", " ")
	if err := os.WriteFile(destination, append(encoded, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d canonical encodings", len(results))
}
