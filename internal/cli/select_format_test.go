package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `cadre select --format` -- the contract, not the layout.
//
// JSON is what every downstream consumer reads: the packaged plugin wrapper,
// the Cline plugins, the pip/pipx distribution, CI. So the default output must
// stay JSON, and adding a text mode must not perturb it.
//
// This is not a hypothetical. select_agents.go's own header records that the
// first Go reimplementation of this command "defaulted to human text where the
// contract defaults to JSON" -- and that regression was caught by
// roster/orchestration/test/test_plan_text_format.py, whose four format tests
// run the *Python* selector as a subprocess. Nothing on the Go side checked it.
// The single regression this command is documented as having shipped was
// guarded only by the implementation being retired.
//
// Every case here writes through --output rather than capturing stdout: it is
// the same code path (runSelectGo renders once and then chooses a sink), and
// it makes the "--output honours --format" case testable at all.

// selectInto runs `cadre select` into a file and returns what was written.
func selectInto(t *testing.T, extra ...string) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "plan.out")
	args := append([]string{
		"--task", "add rate limiting to the login endpoint",
		"--files", "api/auth.go",
		"--task-id", "TASK-FMT",
		"--classification", "internal",
		// Pinned so the plan does not depend on the checkout's git origin.
		"--source", "deagy/cadre",
		"--output", target,
	}, extra...)

	if code := runSelectGo(args); code != 0 {
		t.Fatalf("cadre select %v exited %d", extra, code)
	}
	written, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("nothing was written to --output: %v", err)
	}
	if len(written) == 0 {
		t.Fatal("--output produced an empty file")
	}
	return string(written)
}

// stablePlan drops the fields that differ between two runs of the same input.
func stablePlan(t *testing.T, payload string) string {
	t.Helper()
	var plan map[string]any
	if err := json.Unmarshal([]byte(payload), &plan); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	delete(plan, "generated_at")
	delete(plan, "dispatch_fingerprint")
	if provenance, ok := plan["provenance"].(map[string]any); ok {
		delete(provenance, "git_dirty_paths")
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestTheDefaultOutputIsJSON(t *testing.T) {
	// The property the earlier Go rewrite got wrong.
	written := selectInto(t)
	var plan map[string]any
	if err := json.Unmarshal([]byte(written), &plan); err != nil {
		t.Fatalf("the default output is not JSON, which every downstream "+
			"consumer reads: %v\n%s", err, first(written, 400))
	}
	if plan["schema_version"] == nil || plan["dispatch_fingerprint"] == nil {
		t.Errorf("the default output parsed but is not a plan:\n%s", first(written, 400))
	}
}

func TestAskingForJSONExplicitlyChangesNothing(t *testing.T) {
	// A flag's presence must not perturb the contract output -- otherwise
	// `--format json` and the default are two formats with one name.
	if got, want := stablePlan(t, selectInto(t, "--format", "json")),
		stablePlan(t, selectInto(t)); got != want {
		t.Errorf("--format json differs from the default.\ndefault: %s\nexplicit: %s",
			first(want, 400), first(got, 400))
	}
}

func TestTextFormatIsNotJSONAndNamesTheAgents(t *testing.T) {
	written := selectInto(t, "--format", "text")
	var discard any
	if err := json.Unmarshal([]byte(written), &discard); err == nil {
		t.Errorf("--format text produced JSON:\n%s", first(written, 400))
	}
	if !strings.Contains(written, "AGENTS") {
		t.Errorf("--format text produced something that is not the plan:\n%s",
			first(written, 400))
	}
}

func TestTheOutputFileHonoursTheChosenFormat(t *testing.T) {
	// `--format text --output f` writing JSON into f is the specific bug this
	// guards: the format is chosen once and the sink is chosen separately, so
	// the two can drift apart without either flag looking wrong on its own.
	//
	// Both directions, because a renderer wired to the sink rather than to the
	// flag fails only one of them.
	if text := selectInto(t, "--format", "text"); !strings.Contains(text, "AGENTS") {
		t.Errorf("--format text --output wrote something else:\n%s", first(text, 300))
	}
	written := selectInto(t, "--format", "json")
	var discard any
	if err := json.Unmarshal([]byte(written), &discard); err != nil {
		t.Errorf("--format json --output did not write JSON: %v\n%s",
			err, first(written, 300))
	}
}

func TestAnUnknownFormatIsRefusedByName(t *testing.T) {
	// Falling back to a default would hand a caller who asked for one format
	// a different one, silently. The message is argparse-shaped because the
	// flag surface is a contract of its own.
	target := filepath.Join(t.TempDir(), "plan.out")
	code := runSelectGo([]string{
		"--task", "anything", "--files", "api/auth.go",
		"--format", "yaml", "--output", target,
	})
	if code == 0 {
		t.Fatal("an unknown --format was accepted")
	}
	if _, err := os.Stat(target); err == nil {
		t.Error("a refused invocation still wrote its --output file")
	}
}

func first(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "..."
}
