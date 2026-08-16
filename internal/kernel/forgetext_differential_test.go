package kernel

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
)

// The forge text sanitizers, compared with the Python implementation on the
// same inputs.
//
// These functions are pure, so the comparison can be exhaustive in a way the
// command-level differentials cannot: every input below is run through both
// implementations in one pass and compared on both the verdict and the exact
// message. That matters here because the message is what an operator reads to
// find out which field of which file to fix.
//
// The inputs are chosen to be nasty rather than realistic. Sanitization is
// only interesting at the boundary: the character that renders as nothing, the
// line that a forge reads as a command, the reference that notifies somebody.

type sanitizerOutcome struct {
	OK     bool   `json:"ok"`
	Value  string `json:"value"`
	Reason string `json:"reason"`
}

func TestTheSanitizersAgreeWithThePythonImplementation(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is unavailable, so there is nothing to compare against")
	}
	probes := append(append([]struct {
		function string
		input    string
	}{}, sanitizerProbes...), lengthProbes()...)
	inputs := make([][2]string, 0, len(probes))
	for _, probe := range probes {
		inputs = append(inputs, [2]string{probe.function, probe.input})
	}
	expected := pythonSanitizerOutcomes(t, inputs)

	for index, probe := range probes {
		name := probe.function + ": " + probe.input
		switch {
		case probe.input == "":
			name = probe.function + ": <empty>"
		case len(probe.input) > 40:
			name = fmt.Sprintf("%s: %d characters", probe.function, len([]rune(probe.input)))
		}
		t.Run(name, func(t *testing.T) {
			var value string
			var err error
			if probe.function == "free" {
				value, err = SanitizeFreeText(probe.input, "field")
			} else {
				value, err = SanitizeTitleText(probe.input, "field")
			}

			want := expected[index]
			if want.OK != (err == nil) {
				t.Fatalf("python ok=%v (%s), go ok=%v (%v)", want.OK, want.Reason, err == nil, err)
			}
			if want.OK && value != want.Value {
				t.Errorf("python returned %q, go returned %q", want.Value, value)
			}
			if !want.OK && err.Error() != want.Reason {
				t.Errorf("python refused with %q, go refused with %q", want.Reason, err.Error())
			}
		})
	}
}

// pythonSanitizerOutcomes runs every input through the Python module in one
// process, so the comparison costs one interpreter start rather than forty.
func pythonSanitizerOutcomes(t *testing.T, inputs [][2]string) []sanitizerOutcome {
	t.Helper()
	encoded, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	script := `
import json, sys
from agentic_sdlc import _forge_text

results = []
for function, text in json.loads(sys.argv[1]):
    sanitize = _forge_text.sanitize_free_text if function == "free" else _forge_text.sanitize_title_text
    try:
        results.append({"ok": True, "value": sanitize(text, "field"), "reason": ""})
    except _forge_text.ForgeTextError as error:
        results.append({"ok": False, "value": "", "reason": str(error)})
print(json.dumps(results))
`
	command := exec.Command("python3", "-c", script, string(encoded))
	command.Dir = filepath.Join(repositoryRoot(t), "kernel")
	output, err := command.Output()
	if err != nil {
		t.Skipf("the Python sanitizer could not be run: %v", err)
	}
	var outcomes []sanitizerOutcome
	if err := json.Unmarshal(output, &outcomes); err != nil {
		t.Fatalf("the Python side did not return JSON: %s", output)
	}
	if len(outcomes) != len(inputs) {
		t.Fatalf("expected %d outcomes, got %d", len(inputs), len(outcomes))
	}
	return outcomes
}
