package selector

import "testing"

// These pin the specific ways Go's own JSON encoder differs from Python's,
// because each difference silently changes dispatch_fingerprint and none of
// them is visible in a normal-looking plan.

func TestCanonicalJSONEscapesNonASCIILikePythonEnsureASCII(t *testing.T) {
	// Python's json.dumps defaults to ensure_ascii=True. Go emits raw UTF-8.
	// This is the difference the corpus's unicode case exists to catch.
	got, err := CanonicalJSON(map[string]any{"s": "café"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"s":"caf\u00e9"}`
	if string(got) != want {
		t.Errorf("CanonicalJSON = %s, want %s", got, want)
	}
}

func TestCanonicalJSONWritesAstralRunesAsSurrogatePairs(t *testing.T) {
	got, err := CanonicalJSON(map[string]any{"s": "🚀"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"s":"\ud83d\ude80"}`
	if string(got) != want {
		t.Errorf("CanonicalJSON = %s, want %s", got, want)
	}
}

func TestCanonicalJSONDoesNotEscapeHTMLPunctuation(t *testing.T) {
	// Go's encoding/json escapes <, > and & by default; Python does not
	// escape them at all.
	got, err := CanonicalJSON(map[string]any{"s": "a<b>c&d"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"s":"a<b>c&d"}`
	if string(got) != want {
		t.Errorf("CanonicalJSON = %s, want %s", got, want)
	}
}

func TestCanonicalJSONUsesShortEscapesForControlCharacters(t *testing.T) {
	got, err := CanonicalJSON(map[string]any{"s": "a\tb\nc\x01d"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"s":"a\tb\nc\u0001d"}`
	if string(got) != want {
		t.Errorf("CanonicalJSON = %s, want %s", got, want)
	}
}

func TestCanonicalJSONSortsKeysAndOmitsWhitespace(t *testing.T) {
	got, err := CanonicalJSON(map[string]any{"zebra": 1, "alpha": 2, "Beta": 3})
	if err != nil {
		t.Fatal(err)
	}
	// Sorted by code point, so uppercase sorts before lowercase.
	want := `{"Beta":3,"alpha":2,"zebra":1}`
	if string(got) != want {
		t.Errorf("CanonicalJSON = %s, want %s", got, want)
	}
}

func TestCanonicalJSONKeepsIntegersIntegral(t *testing.T) {
	// A float64 that happens to be integral must not gain a fractional part
	// or an exponent, or every version number in the plan changes.
	got, err := CanonicalJSON(map[string]any{"v": float64(1), "big": float64(1000000)})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"big":1000000,"v":1}`
	if string(got) != want {
		t.Errorf("CanonicalJSON = %s, want %s", got, want)
	}
}

func TestCanonicalJSONSortsStructFieldsToo(t *testing.T) {
	// encoding/json emits struct fields in declaration order. Routing
	// everything through the generic form is what makes ordering depend on
	// the key rather than on which Go shape the value happened to have.
	got, err := CanonicalJSON(Disposition{Status: "staffed", Reason: "because"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"reason":"because","status":"staffed"}`
	if string(got) != want {
		t.Errorf("CanonicalJSON = %s, want %s -- struct fields must sort by key", got, want)
	}
}

func TestDispatchFingerprintExcludesVolatileKeys(t *testing.T) {
	base := map[string]any{"task_id": "T1", "workflow": "new-service"}

	stable, err := DispatchFingerprint(base)
	if err != nil {
		t.Fatal(err)
	}

	// generated_at and provenance vary by generation-time environment, so a
	// fingerprint that moved with them would be a checksum over noise.
	withVolatile := map[string]any{
		"task_id": "T1", "workflow": "new-service",
		"generated_at":         "2026-01-01T00:00:00.000Z",
		"provenance":           map[string]any{"git_commit_sha": "abc"},
		"dispatch_fingerprint": "sha256:whatever",
	}
	got, err := DispatchFingerprint(withVolatile)
	if err != nil {
		t.Fatal(err)
	}
	if got != stable {
		t.Errorf("fingerprint changed when only volatile keys differed:\n  %s\n  %s", stable, got)
	}

	// But a real content change must move it.
	changed := map[string]any{"task_id": "T1", "workflow": "rollback"}
	moved, err := DispatchFingerprint(changed)
	if err != nil {
		t.Fatal(err)
	}
	if moved == stable {
		t.Error("fingerprint did not change when the plan's content did")
	}
}
