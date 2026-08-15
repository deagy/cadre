package selector

import (
	"encoding/json"
	"os"
	"testing"
)

// Env-gated parity probes for the human-facing output, driven by
// roster/orchestration/test/probe_text_parity.py.

type wrapProbeCase struct {
	Text       string `json:"text"`
	Width      int    `json:"width"`
	Initial    string `json:"initial"`
	Subsequent string `json:"subsequent"`
}

func TestTextwrapParityProbe(t *testing.T) {
	inputPath := os.Getenv("CADRE_WRAP_PROBE_IN")
	outputPath := os.Getenv("CADRE_WRAP_PROBE_OUT")
	if inputPath == "" || outputPath == "" {
		t.Skip("set CADRE_WRAP_PROBE_IN and CADRE_WRAP_PROBE_OUT to run the parity probe")
	}

	raw, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	var cases []wrapProbeCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}

	answers := make([]string, 0, len(cases))
	for _, probe := range cases {
		answers = append(answers, textwrapFill(probe.Text, probe.Width, probe.Initial, probe.Subsequent))
	}

	encoded, err := json.MarshalIndent(answers, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputPath, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d answers", len(answers))
}

func TestPlanTextParityProbe(t *testing.T) {
	inputPath := os.Getenv("CADRE_PLANTEXT_PROBE_IN")
	outputPath := os.Getenv("CADRE_PLANTEXT_PROBE_OUT")
	if inputPath == "" || outputPath == "" {
		t.Skip("set CADRE_PLANTEXT_PROBE_IN and CADRE_PLANTEXT_PROBE_OUT to run the parity probe")
	}

	raw, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	var plans []map[string]any
	if err := json.Unmarshal(raw, &plans); err != nil {
		t.Fatal(err)
	}

	answers := make([]string, 0, len(plans))
	for _, plan := range plans {
		answers = append(answers, FormatPlanText(plan))
	}

	encoded, err := json.MarshalIndent(answers, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputPath, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d answers", len(answers))
}

type nearMissProbeCase struct {
	Task    string         `json:"task"`
	Matched []string       `json:"matched"`
	Config  map[string]any `json:"config"`
}

func TestNearMissParityProbe(t *testing.T) {
	inputPath := os.Getenv("CADRE_NEARMISS_PROBE_IN")
	outputPath := os.Getenv("CADRE_NEARMISS_PROBE_OUT")
	if inputPath == "" || outputPath == "" {
		t.Skip("set CADRE_NEARMISS_PROBE_IN and CADRE_NEARMISS_PROBE_OUT to run the parity probe")
	}

	raw, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	var cases []nearMissProbeCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}

	answers := make([]string, 0, len(cases))
	for _, probe := range cases {
		matched := map[string]bool{}
		for _, id := range probe.Matched {
			matched[id] = true
		}
		answers = append(answers, FormatNearMissesText(FindNearMisses(probe.Config, probe.Task, matched)))
	}

	encoded, err := json.MarshalIndent(answers, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputPath, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d answers", len(answers))
}
