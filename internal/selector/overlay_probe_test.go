package selector

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Env-gated parity probe for the overlay merge, driven by
// roster/orchestration/test/probe_overlay_parity.py.
//
// Both the accepted result and the refusal are compared. A port that merged
// correctly but refused a different set of documents would be a different
// mechanism wearing the same name -- and it is the refusals that carry the
// gating semantics, so they matter more than the merges.

type overlayProbeAnswer struct {
	Merged string `json:"merged"`
	Error  string `json:"error"`
}

func TestOverlayParityProbe(t *testing.T) {
	inputPath := os.Getenv("CADRE_OVERLAY_PROBE_IN")
	outputPath := os.Getenv("CADRE_OVERLAY_PROBE_OUT")
	basePath := os.Getenv("CADRE_OVERLAY_PROBE_BASE")
	if inputPath == "" || outputPath == "" || basePath == "" {
		t.Skip("set CADRE_OVERLAY_PROBE_IN/OUT/BASE to run the parity probe")
	}

	baseRaw, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	// The overlays arrive as raw JSON text, not decoded, so a malformed
	// document is a case rather than something the harness cannot carry.
	var overlays []string
	if err := json.Unmarshal(raw, &overlays); err != nil {
		t.Fatal(err)
	}

	answers := make([]overlayProbeAnswer, 0, len(overlays))
	for _, overlayText := range overlays {
		// A fresh base per case: the merge copies rather than mutates, and a
		// probe that shared one decoded base would hide it if that stopped
		// being true.
		var base map[string]any
		if err := json.Unmarshal(baseRaw, &base); err != nil {
			t.Fatal(err)
		}

		answer := overlayProbeAnswer{}
		merged, err := mergeOverlayText(base, overlayText)
		if err != nil {
			answer.Error = err.Error()
		} else {
			canonical, err := CanonicalJSON(merged)
			if err != nil {
				t.Fatal(err)
			}
			answer.Merged = string(canonical)
		}
		answers = append(answers, answer)
	}

	encoded, err := json.MarshalIndent(answers, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputPath, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d answers to %s", len(answers), outputPath)
}

func mergeOverlayText(base map[string]any, overlayText string) (map[string]any, error) {
	var loaded any
	decoder := json.NewDecoder(strings.NewReader(overlayText))
	decoder.UseNumber()
	if err := decoder.Decode(&loaded); err != nil {
		return nil, overlayErrorf("malformed overlay JSON")
	}
	overlay, ok := loaded.(map[string]any)
	if !ok {
		return nil, overlayErrorf("overlay root must be a JSON object")
	}
	merged, err := MergeRouting(base, overlay)
	if err != nil {
		return nil, err
	}
	if err := ValidateRoutingConfig(merged); err != nil {
		return nil, overlayErrorf("effective configuration failed validation: %s", err)
	}
	return merged, nil
}
