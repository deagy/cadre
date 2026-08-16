package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `cadre select --explain` -- a diagnostic that must not disturb what it
// explains.
//
// The flag prints near-miss routing reasoning: which routes almost matched and
// which keyword was missing. It exists for a person staring at a needs-triage
// plan wondering why. select_go.go states the contract in a comment:
//
//	Printed to stderr, after the machine-readable plan, and derived only from
//	data the plan already exposes plus the effective routing config. It never
//	touches the plan or the rendered bytes, so the output is byte-identical
//	with and without --explain.
//
// Nothing checked any of it. The Python side's four --explain tests run
// select_agents.py as a subprocess, so they go with it.
//
// The property is worth a test on its own terms: a diagnostic flag that writes
// to stdout corrupts the JSON every downstream consumer parses, and it does so
// only for the person who reached for the diagnostic -- so it survives every
// run that did not.

// selectCapturingStderr runs the command with the plan going to a file and
// stderr redirected, and returns both.
func selectCapturingStderr(t *testing.T, extra ...string) (plan, stderr string) {
	t.Helper()
	directory := t.TempDir()
	planPath := filepath.Join(directory, "plan.json")
	errPath := filepath.Join(directory, "stderr.txt")

	handle, err := os.Create(errPath)
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = handle
	code := runSelectGo(append([]string{
		// A task that matches nothing, so there are near misses to report.
		// A staffed plan can legitimately have none.
		"--task", "reticulate the splines",
		"--files", "docs/notes.txt",
		"--task-id", "TASK-EXPLAIN",
		"--classification", "internal",
		"--source", "deagy/cadre",
		"--output", planPath,
	}, extra...))
	os.Stderr = saved
	if closeErr := handle.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if code != 0 {
		t.Fatalf("cadre select %v exited %d", extra, code)
	}

	planBytes, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatalf("no plan was written: %v", err)
	}
	errBytes, err := os.ReadFile(errPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(planBytes), string(errBytes)
}

// withoutVolatile drops the two fields that differ between two runs of the
// same input, so the rest can be compared byte-for-byte.
func withoutVolatile(t *testing.T, payload string) string {
	t.Helper()
	var plan map[string]any
	if err := json.Unmarshal([]byte(payload), &plan); err != nil {
		t.Fatalf("the plan is not JSON: %v", err)
	}
	delete(plan, "generated_at")
	if provenance, ok := plan["provenance"].(map[string]any); ok {
		delete(provenance, "git_dirty_paths")
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestExplainDoesNotChangeThePlanItExplains(t *testing.T) {
	// dispatch_fingerprint is deliberately NOT excluded here. It is a hash
	// over the plan's own canonical form, so leaving it in means the
	// comparison covers every field the fingerprint covers -- including any
	// the JSON comparison below might normalize away.
	plain, _ := selectCapturingStderr(t)
	explained, _ := selectCapturingStderr(t, "--explain")

	if got, want := withoutVolatile(t, explained), withoutVolatile(t, plain); got != want {
		t.Errorf("--explain changed the plan.\nwithout: %s\nwith:    %s", want, got)
	}

	var withFlag, withoutFlag map[string]any
	if err := json.Unmarshal([]byte(explained), &withFlag); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(plain), &withoutFlag); err != nil {
		t.Fatal(err)
	}
	if withFlag["dispatch_fingerprint"] != withoutFlag["dispatch_fingerprint"] {
		t.Errorf("dispatch_fingerprint differs with --explain: %v vs %v",
			withFlag["dispatch_fingerprint"], withoutFlag["dispatch_fingerprint"])
	}
}

func TestExplainWritesToStderrAndNothingToStdout(t *testing.T) {
	// The direction that matters. Near-miss reasoning on stdout would be
	// prepended or appended to the JSON document every consumer parses --
	// breaking them only for the person who asked for the diagnostic, which
	// is exactly the run least likely to be automated and noticed.
	planWithout, stderrWithout := selectCapturingStderr(t)
	planWith, stderrWith := selectCapturingStderr(t, "--explain")

	if strings.TrimSpace(stderrWithout) != "" {
		t.Errorf("the command explained itself without being asked:\n%s", stderrWithout)
	}
	if strings.TrimSpace(stderrWith) == "" {
		t.Fatal("--explain produced no explanation, so the rest proves nothing")
	}

	// Whatever went to stderr is not in the plan.
	for _, plan := range []string{planWith, planWithout} {
		var discard any
		if err := json.Unmarshal([]byte(plan), &discard); err != nil {
			t.Errorf("the plan is not parseable JSON: %v", err)
		}
	}
	firstLine := strings.SplitN(strings.TrimSpace(stderrWith), "\n", 2)[0]
	if firstLine != "" && strings.Contains(planWith, firstLine) {
		t.Errorf("the explanation leaked into the plan document.\n"+
			"stderr began: %q", firstLine)
	}
}

func TestExplainLeavesStdoutPureJSONWithNoOutputFile(t *testing.T) {
	// The cases above route the plan through --output, which is the only way
	// to read it and stderr separately -- but it also means stdout is not the
	// sink under test there. Without --output the plan goes to stdout, which
	// is the shape every consumer actually uses (`cadre select ... | jq`), and
	// the one an explanation printed to the wrong stream would break.
	//
	// So: capture stdout itself, and require that what a pipe would receive
	// still parses as exactly one JSON document.
	directory := t.TempDir()
	outPath := filepath.Join(directory, "stdout.txt")
	errPath := filepath.Join(directory, "stderr.txt")

	outHandle, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	errHandle, err := os.Create(errPath)
	if err != nil {
		t.Fatal(err)
	}
	savedOut, savedErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outHandle, errHandle
	code := runSelectGo([]string{
		"--task", "reticulate the splines",
		"--files", "docs/notes.txt",
		"--task-id", "TASK-EXPLAIN-PIPE",
		"--classification", "internal",
		"--source", "deagy/cadre",
		"--explain",
	})
	os.Stdout, os.Stderr = savedOut, savedErr
	if err := outHandle.Close(); err != nil {
		t.Fatal(err)
	}
	if err := errHandle.Close(); err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("cadre select --explain exited %d", code)
	}

	stdout, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.ReadFile(errPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(stderr)) == "" {
		t.Fatal("--explain produced no explanation, so this proves nothing")
	}

	// A decoder rather than Unmarshal: Unmarshal accepts trailing whitespace
	// but rejects trailing content, while a decoder can be asked explicitly
	// whether anything followed the document -- which is what an explanation
	// appended to the plan would be.
	decoder := json.NewDecoder(strings.NewReader(string(stdout)))
	var plan map[string]any
	if err := decoder.Decode(&plan); err != nil {
		t.Fatalf("stdout is not a JSON document: %v\n%s", err, explainExcerpt(string(stdout), 400))
	}
	if plan["dispatch_fingerprint"] == nil {
		t.Errorf("stdout parsed but is not a plan:\n%s", explainExcerpt(string(stdout), 400))
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		t.Errorf("something follows the plan on stdout; the explanation "+
			"belongs on stderr:\n%v", trailing)
	}
	if rest, _ := io.ReadAll(decoder.Buffered()); strings.TrimSpace(string(rest)) != "" {
		t.Errorf("trailing bytes after the plan on stdout: %q", explainExcerpt(string(rest), 200))
	}
}

func TestTheExplanationCarriesNoNumericScore(t *testing.T) {
	// A deliberate design choice, not an accident of the current text: the
	// near-miss report names which keywords matched and which are missing,
	// and offers no score. A number invites tuning a threshold nobody
	// designed, and reads as a ranking the selector does not compute -- the
	// selector is deterministic rule matching, and a "0.67 match" would
	// suggest otherwise.
	_, explanation := selectCapturingStderr(t, "--explain")
	if strings.TrimSpace(explanation) == "" {
		t.Fatal("no explanation to check")
	}
	for _, digit := range []string{"0.", "%", "score", "Score", "rank", "confidence"} {
		if strings.Contains(explanation, digit) {
			t.Errorf("the explanation carries %q, which reads as a ranking the "+
				"selector does not compute:\n%s", digit, explanation)
		}
	}
}

// explainExcerpt trims a long payload for a failure message. Named distinctly
// rather than reusing a helper from another file in this package: two test
// files each defining `first` compile fine apart and not together.
func explainExcerpt(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "..."
}
