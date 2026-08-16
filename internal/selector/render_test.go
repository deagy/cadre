package selector

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// RenderPlanJSON: the wire document, as distinct from the hashed payload.
//
// A dispatch plan is encoded twice, by two encoders, on purpose:
//
//	RenderPlanJSON  -> what `cadre select` prints. Plan key order, indent 2,
//	                   raw UTF-8, trailing newline.
//	CanonicalJSON   -> what dispatch_fingerprint hashes. Sorted keys, no
//	                   whitespace, non-ASCII escaped.
//
// Nothing gated the first one. It is natural to assume the Python differential
// did -- test_select_differential.py has a test called
// `test_go_selector_matches_the_python_plan_byte_for_byte` -- but that test
// runs `json.loads` on both implementations' stdout and compares the parsed
// dicts. It compares plans, not bytes. Every property below could have broken
// with that suite green.
//
// So this is a first gate rather than a replacement for one, and the gap it
// closes predates the Python selector's removal rather than being caused by it.
//
// The rendering was verified against `json.dumps(plan, indent=2,
// ensure_ascii=False) + "\n"` across all 25 cases of select_corpus.json on
// 2026-08-15, while the Python selector was still in the tree. That check
// cannot be re-run once it is gone, which is why the result is pinned as
// testdata below rather than left as a comparison.

// renderableCase builds one plan with every volatile field pinned, so the
// rendering is a function of the selector rather than of the clock.
func renderableCase(t *testing.T) map[string]any {
	t.Helper()
	plan, err := BuildDispatchPlan(loadRoutingConfig(t), PlanInput{
		Task:              "Update the React navigation for keyboard accessibility",
		TaskID:            "RENDER-GOLDEN",
		ChangedFiles:      []string{"frontend/src/Nav.tsx"},
		Classification:    "internal",
		ChangedFileSource: "explicit",
		Sources:           []string{"deagy/cadre", "proposed-knowledge"},
		RepositoryRoot:    "<REPO_ROOT>",
	}, PlanOptions{
		Catalog:    loadCatalogIDs(t),
		Gates:      loadLifecycleContract(t).Gates,
		RosterRoot: filepath.Join(selectorRepoRoot(t), "roster"),
		// Placeholders, so the recorded document carries no absolute path and
		// reads the same in every checkout. Left to default, the knowledge
		// invocation's argv[0] comes out as "", which in a committed golden is
		// indistinguishable from a bug.
		KnowledgeCLI: "<KNOWLEDGE_CLI>",
		Provenance:   map[string]any{"git_commit_sha": "<SHA>", "git_dirty_paths": []any{}},
	})
	if err != nil {
		t.Fatalf("building the plan: %v", err)
	}
	if plan["status"] != "ready" {
		t.Fatalf("the case resolved to %v, so it renders a near-empty plan and "+
			"exercises almost none of the encoder", plan["status"])
	}
	// generated_at is wall-clock and excluded from the fingerprint, so pinning
	// it changes nothing the renderer is being held to. dispatch_fingerprint is
	// deliberately left alone: it is derived from the inputs above, which are
	// fixed, so the golden pins it too.
	plan["generated_at"] = "2026-01-01T00:00:00.000Z"
	return plan
}

func TestTheRenderedPlanIsTheRecordedDocument(t *testing.T) {
	rendered, err := RenderPlanJSON(renderableCase(t))
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	golden := filepath.Join("testdata", "rendered_plan.json")
	if os.Getenv("CADRE_RENDER_GOLDEN_REGENERATE") != "" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, rendered, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Fatal("golden regenerated; re-read the diff before committing it -- " +
			"a golden refreshed without being read pins whatever the bug produced")
	}
	recorded, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("reading the recorded document (regenerate with "+
			"CADRE_RENDER_GOLDEN_REGENERATE=1): %v", err)
	}
	if string(rendered) != string(recorded) {
		wasRecorded, wasRendered := firstDifference(string(recorded), string(rendered))
		t.Errorf("the printed plan changed.\nrecorded: %s\nrendered: %s",
			wasRecorded, wasRendered)
	}
}

func TestTheRenderedPlanKeepsPlanOrderRatherThanSorting(t *testing.T) {
	// The whole reason RenderPlanJSON exists rather than calling
	// encoding/json.MarshalIndent. Sorted output would still parse, still
	// fingerprint identically, and still pass every structural test -- while
	// putting `agents` above `task_id` in something people read.
	rendered, err := RenderPlanJSON(renderableCase(t))
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	var order []string
	for _, line := range strings.Split(string(rendered), "\n") {
		if strings.HasPrefix(line, `  "`) {
			key, _, _ := strings.Cut(strings.TrimPrefix(line, `  "`), `"`)
			order = append(order, key)
		}
	}
	if len(order) < 5 {
		t.Fatalf("read %d top-level keys; this test would prove nothing", len(order))
	}
	var wanted []string
	present := map[string]bool{}
	for _, key := range order {
		present[key] = true
	}
	for _, key := range PlanKeyOrder {
		if present[key] {
			wanted = append(wanted, key)
		}
	}
	if strings.Join(order, ",") != strings.Join(wanted, ",") {
		t.Errorf("top-level keys came out as\n  %v\nrather than PlanKeyOrder's\n  %v",
			order, wanted)
	}
	if sorted := sortedSlice(order); strings.Join(order, ",") == strings.Join(sorted, ",") {
		t.Error("plan order and alphabetical order coincide here, so this test " +
			"cannot tell them apart and proves nothing")
	}
}

func TestTheRenderedPlanRoundTrips(t *testing.T) {
	// Order and indentation are cosmetic to a consumer that parses; being
	// parseable at all is not. An encoder that emitted a trailing comma or an
	// unquoted key would satisfy the ordering test above.
	plan := renderableCase(t)
	rendered, err := RenderPlanJSON(plan)
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	var reparsed map[string]any
	if err := json.Unmarshal(rendered, &reparsed); err != nil {
		t.Fatalf("the printed plan does not parse as JSON: %v", err)
	}
	viaRenderer, err := CanonicalJSON(reparsed)
	if err != nil {
		t.Fatal(err)
	}
	viaPlan, err := CanonicalJSON(plan)
	if err != nil {
		t.Fatal(err)
	}
	if string(viaRenderer) != string(viaPlan) {
		wasPlan, wasParsed := firstDifference(string(viaPlan), string(viaRenderer))
		t.Errorf("the printed plan parses back to a different plan.\n"+
			"plan:   %s\nparsed: %s", wasPlan, wasParsed)
	}
	if !strings.HasSuffix(string(rendered), "}\n") {
		t.Error("the document does not end in a newline; consumers append to a stream")
	}
	if strings.Contains(string(rendered), "\n\t") {
		t.Error("indented with tabs; json.dumps(indent=2) emits spaces")
	}
}

func TestNonASCIIIsRawInTheDocumentAndEscapedInTheFingerprint(t *testing.T) {
	// The asymmetry RenderPlanJSON's own comment calls load-bearing, and which
	// nothing exercised. A task description is free text from a human, so this
	// is reachable input, not a contrived one.
	//
	// It matters in both directions. Escaping the document would change bytes
	// people read and diff for no reason; *not* escaping the canonical form
	// would make dispatch_fingerprint depend on the encoder's Unicode handling
	// rather than on the plan, so two correct implementations could hash the
	// same plan differently.
	plan := map[string]any{
		"schema_version": SchemaVersion,
		"task_id":        "UNICODE-1",
		"status":         "ready",
		"inputs":         map[string]any{"task": "café — naïve <resumé> & Ω"},
	}
	rendered, err := RenderPlanJSON(plan)
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	for _, literal := range []string{"café", "—", "naïve", "Ω", "<resumé>", "&"} {
		if !strings.Contains(string(rendered), literal) {
			t.Errorf("the document escaped %q; json.dumps(ensure_ascii=False) "+
				"emits it raw, and encoding/json would escape < > & as well",
				literal)
		}
	}
	if strings.Contains(string(rendered), `\u`) {
		t.Errorf("the document contains a \\u escape:\n%s", rendered)
	}

	canonical, err := CanonicalJSON(plan)
	if err != nil {
		t.Fatalf("canonicalising: %v", err)
	}
	for _, escape := range []string{`caf\u00e9`, `\u2014`, `na\u00efve`, `\u03a9`} {
		if !strings.Contains(string(canonical), escape) {
			t.Errorf("the canonical form did not escape non-ASCII as %s, so the "+
				"fingerprint now depends on encoder Unicode handling:\n%s",
				escape, canonical)
		}
	}
	if strings.ContainsAny(string(canonical), "éàΩ—") {
		t.Errorf("the canonical form carries raw non-ASCII:\n%s", canonical)
	}
	// And `<` `>` `&` go the other way: encoding/json escapes them by default
	// and json.dumps does not, so both encoders have to suppress that.
	if !strings.Contains(string(canonical), "<resum") || !strings.Contains(string(canonical), "& ") {
		t.Errorf("the canonical form HTML-escaped < > or &:\n%s", canonical)
	}
}

func TestControlCharactersEscapeTheWayJSONDumpsEscapesThem(t *testing.T) {
	// A task description is free text: someone pastes a multi-line ticket body
	// and the plan carries a newline. Python emits the five shorthand escapes
	// and \uXXXX for every other control character, so a plan rendered here has
	// to as well -- and these were the branches nothing reached.
	//
	// The expectation is not derived from Go's encoder. It is what
	// `json.dumps({"k": s}, indent=2, ensure_ascii=False)` printed for this
	// exact string, recorded 2026-08-15 while the Python selector was in tree.
	subject := "tab\there\nnewline \"quoted\" back\\slash \b\f\r ctrl\u0001\u001f end"
	rendered, err := RenderPlanJSON(map[string]any{"task_id": subject})
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	const wanted = "{\n  \"task_id\": \"tab\\there\\nnewline \\\"quoted\\\" back\\\\slash " +
		"\\b\\f\\r ctrl\\u0001\\u001f end\"\n}\n"
	if string(rendered) != wanted {
		t.Errorf("control characters render differently from json.dumps.\n"+
			"json.dumps: %q\nrendered:   %q", wanted, string(rendered))
	}
}

func sortedSlice(values []string) []string {
	out := append([]string(nil), values...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
