package canonicaljson_test

import (
	"testing"

	"github.com/deagy/cadre/cli/internal/kernel"
	"github.com/deagy/cadre/cli/internal/selector"
)

// The selector and the kernel must fingerprint a dispatch plan identically.
//
// This is not a style rule. `cadre select` writes a plan and stamps it with a
// fingerprint; `agentic-sdlc validate` recomputes that fingerprint from the
// plan's own content and rejects the plan if it differs. The two sides had
// already disagreed once, over whether `provenance` belongs in the hashed
// payload, and the consequence was total: the kernel rejected every plan the
// selector produced, and the error said the plan had been tampered with.
//
// The two implementations stay separate -- each exclusion set is part of its
// own side's contract -- so this test is the thing holding them together. It
// lives in neither package for that reason: put it in one and it becomes that
// side's opinion of itself.

// planWithEverything is shaped like a real plan, and specifically carries all
// three of the excluded keys. A plan without them agrees trivially, which is
// how the original disagreement survived as long as it did.
func planWithEverything() map[string]any {
	return map[string]any{
		"schema_version":       1,
		"task_id":              "TASK-42",
		"generated_at":         "2026-08-15T09:00:00+00:00",
		"dispatch_fingerprint": "sha256:stale",
		"provenance": map[string]any{
			"working_tree": "dirty",
			"changed_files": []any{
				"internal/kernel/validate.go", "roster/catalog.yaml",
			},
		},
		"inputs": map[string]any{
			"task":           "add an endpoint",
			"classification": "internal",
			"changed_files":  []any{"a.go", "b.tsx"},
		},
		"agents": map[string]any{
			"primary":   []any{"backend-engineer"},
			"reviewers": []any{"code-reviewer"},
		},
		"required_quality_gates": []any{
			map[string]any{"id": "G1", "name": "Intent"},
		},
		"escaping": "a <b> & cé \U0001f680",
		"counts":   map[string]any{"agents": float64(2), "gates": float64(1)},
	}
}

func TestTheSelectorAndTheKernelFingerprintAPlanIdentically(t *testing.T) {
	plan := planWithEverything()

	fromSelector, err := selector.DispatchFingerprint(plan)
	if err != nil {
		t.Fatalf("selector: %v", err)
	}
	fromKernel, err := kernel.DispatchFingerprint(plan)
	if err != nil {
		t.Fatalf("kernel: %v", err)
	}
	if fromSelector != fromKernel {
		t.Fatalf("the two sides disagree:\n  selector %s\n  kernel   %s", fromSelector, fromKernel)
	}
}

func TestNeitherSideHashesGeneratedMetadata(t *testing.T) {
	// Self-check on the test above: if both sides hashed everything, they
	// would still agree with each other, and agreement would prove nothing.
	// What makes the fingerprint a determinism check rather than a checksum
	// over environment noise is that changing only the excluded keys changes
	// nothing.
	plan := planWithEverything()
	// Each side is compared against its own baseline, so a failure here says
	// "this side hashes metadata" rather than borrowing the other side's
	// answer and reporting both as broken.
	kernelBaseline, err := kernel.DispatchFingerprint(plan)
	if err != nil {
		t.Fatal(err)
	}
	selectorBaseline, err := selector.DispatchFingerprint(plan)
	if err != nil {
		t.Fatal(err)
	}

	plan["generated_at"] = "2027-01-01T00:00:00+00:00"
	plan["dispatch_fingerprint"] = "sha256:something-else"
	plan["provenance"] = map[string]any{"working_tree": "clean"}

	afterKernel, err := kernel.DispatchFingerprint(plan)
	if err != nil {
		t.Fatal(err)
	}
	afterSelector, err := selector.DispatchFingerprint(plan)
	if err != nil {
		t.Fatal(err)
	}
	if afterKernel != kernelBaseline {
		t.Error("the kernel's fingerprint changed when only generated metadata did")
	}
	if afterSelector != selectorBaseline {
		t.Error("the selector's fingerprint changed when only generated metadata did")
	}

	// And the other direction: a decision-relevant change must move it, or
	// the exclusions have swallowed the plan.
	plan["agents"] = map[string]any{"primary": []any{"someone-else"}, "reviewers": []any{}}
	changed, err := kernel.DispatchFingerprint(plan)
	if err != nil {
		t.Fatal(err)
	}
	if changed == kernelBaseline {
		t.Error("changing the selected agents did not change the fingerprint")
	}
}
