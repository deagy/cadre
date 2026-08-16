package selector

import (
	"strings"
	"testing"
)

// What dispatch_fingerprint is a fingerprint *of*.
//
// It is a SHA-256 over the plan's own canonical form with three keys removed:
// generated_at, provenance, and the fingerprint itself. Consumers compare
// plans by it -- two runs agreeing means the selector made the same decision.
//
// The exclusions are what make that true. generated_at is wall-clock and
// provenance carries git_dirty_paths, so including either would make the
// fingerprint a checksum over environment noise: every run would differ from
// every other, and a real change in routing would be indistinguishable from
// someone having edited an unrelated file in their working tree.
//
// The exclusion had no direct test. FingerprintExcludedKeys is read by
// plan_probe_test.go, but reading the list is not the same as checking that
// the hash respects it -- a DispatchFingerprint that ignored the list entirely
// would leave that test green.
//
// Ported from roster/orchestration/test/test_provenance.py's
// FingerprintExclusionTests, which tests the Python selector and goes with it.

func planForFingerprint() map[string]any {
	return map[string]any{
		"schema_version": SchemaVersion,
		"task_id":        "FP-1",
		"status":         "ready",
		"workflow":       "new-service",
		"inputs": map[string]any{
			"task": "add rate limiting", "changed_files": []any{"api/auth.go"},
		},
		"matched_routes": []any{map[string]any{"id": "backend"}},
		"agents":         map[string]any{"primary": []any{"backend-engineer"}},
	}
}

func fingerprintOf(t *testing.T, plan map[string]any) string {
	t.Helper()
	fingerprint, err := DispatchFingerprint(plan)
	if err != nil {
		t.Fatalf("DispatchFingerprint: %v", err)
	}
	if !strings.HasPrefix(fingerprint, "sha256:") {
		t.Fatalf("fingerprint is not labelled sha256: %q", fingerprint)
	}
	return fingerprint
}

func TestTheFingerprintIgnoresEveryExcludedKey(t *testing.T) {
	// One case per excluded key, with a value that would certainly change a
	// hash that saw it. Asserting only on provenance would leave the other two
	// resting on the same list nothing checks.
	bare := planForFingerprint()
	baseline := fingerprintOf(t, bare)

	for _, probe := range []struct {
		key   string
		value any
	}{
		{"generated_at", "2026-08-16T12:34:56.789Z"},
		{"provenance", map[string]any{
			"git_commit_sha":       "0123456789abcdef0123456789abcdef01234567",
			"git_dirty_paths":      []any{"roster/catalog.yaml"},
			"catalog_content_hash": "sha256:" + strings.Repeat("a", 64),
		}},
		{"dispatch_fingerprint", "sha256:" + strings.Repeat("b", 64)},
	} {
		t.Run("with "+probe.key, func(t *testing.T) {
			plan := planForFingerprint()
			plan[probe.key] = probe.value
			if got := fingerprintOf(t, plan); got != baseline {
				t.Errorf("adding %s changed the fingerprint.\nwithout: %s\nwith:    %s",
					probe.key, baseline, got)
			}
		})
	}

	// And all three at once, since a hash could ignore each individually while
	// mishandling their combination -- excluding by position, say.
	together := planForFingerprint()
	together["generated_at"] = "2026-08-16T12:34:56.789Z"
	together["provenance"] = map[string]any{"git_dirty_paths": []any{"a", "b"}}
	together["dispatch_fingerprint"] = "sha256:" + strings.Repeat("c", 64)
	if got := fingerprintOf(t, together); got != baseline {
		t.Errorf("all three excluded keys together changed the fingerprint:\n%s\n%s",
			baseline, got)
	}
}

func TestTwoRunsInDifferentWorkingTreesFingerprintTheSame(t *testing.T) {
	// The property the exclusion exists for, stated as the scenario rather
	// than the mechanism: the same selection made on a clean checkout and on
	// one with unrelated local edits is the same selection, and has to compare
	// equal. If it did not, "the fingerprint changed" would stop meaning
	// "the decision changed" and consumers would learn to ignore it.
	clean := planForFingerprint()
	clean["generated_at"] = "2026-01-01T00:00:00.000Z"
	clean["provenance"] = map[string]any{
		"git_commit_sha": "abc123", "git_dirty_paths": []any{},
	}

	dirty := planForFingerprint()
	dirty["generated_at"] = "2026-08-16T23:59:59.999Z"
	dirty["provenance"] = map[string]any{
		"git_commit_sha":  "abc123",
		"git_dirty_paths": []any{"README.md", "some/unrelated/file.go"},
	}

	if fingerprintOf(t, clean) != fingerprintOf(t, dirty) {
		t.Error("the same decision fingerprinted differently on a dirty tree")
	}
}

func TestTheFingerprintStillSeesEverythingElse(t *testing.T) {
	// The other half, and the reason the test above is not vacuous: a
	// fingerprint that ignored *everything* would satisfy every case so far.
	// Each field below is one a consumer would want a changed fingerprint for.
	baseline := fingerprintOf(t, planForFingerprint())

	for _, probe := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"a different task", func(plan map[string]any) {
			plan["inputs"].(map[string]any)["task"] = "something else entirely"
		}},
		{"a different changed file", func(plan map[string]any) {
			plan["inputs"].(map[string]any)["changed_files"] = []any{"frontend/App.tsx"}
		}},
		{"one more agent", func(plan map[string]any) {
			plan["agents"] = map[string]any{
				"primary": []any{"backend-engineer", "security-reviewer"},
			}
		}},
		{"a different matched route", func(plan map[string]any) {
			plan["matched_routes"] = []any{map[string]any{"id": "frontend"}}
		}},
		{"a different status", func(plan map[string]any) {
			plan["status"] = "needs-triage"
		}},
		{"a different workflow", func(plan map[string]any) {
			plan["workflow"] = "production-release"
		}},
		{"a different task id", func(plan map[string]any) {
			plan["task_id"] = "FP-2"
		}},
		{"a bumped schema version", func(plan map[string]any) {
			plan["schema_version"] = SchemaVersion + 1
		}},
	} {
		t.Run(probe.name, func(t *testing.T) {
			plan := planForFingerprint()
			probe.change(plan)
			if got := fingerprintOf(t, plan); got == baseline {
				t.Errorf("%s did not change the fingerprint", probe.name)
			}
		})
	}
}

func TestTheExcludedKeyListIsTheOneTheHashUses(t *testing.T) {
	// Guards the guard. The cases above name their keys as literals, so a key
	// added to FingerprintExcludedKeys without a case here would go unchecked
	// -- and a key removed from it would be checked by a case that now asserts
	// the wrong thing.
	expected := map[string]bool{
		"generated_at": true, "provenance": true, "dispatch_fingerprint": true,
	}
	if len(FingerprintExcludedKeys) != len(expected) {
		t.Fatalf("FingerprintExcludedKeys = %v; add a case above for any new key, "+
			"and be sure excluding it is what you meant", FingerprintExcludedKeys)
	}
	for _, key := range FingerprintExcludedKeys {
		if !expected[key] {
			t.Errorf("%q is excluded from the fingerprint and has no case above", key)
		}
	}
}
