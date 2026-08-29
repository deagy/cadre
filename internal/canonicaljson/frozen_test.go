package canonicaljson_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/deagy/cadre/cli/internal/selector"
)

// The selector's half of the fingerprint agreement.
//
// `cadre select` writes a dispatch plan and stamps it with a fingerprint;
// `agentic-sdlc validate` recomputes that fingerprint from the plan's own
// content and rejects the plan if it differs. The two implementations are
// deliberately separate -- each exclusion set is part of its own side's
// contract -- and they now live in different repositories, so neither can see
// the other to check.
//
// They had already disagreed once, over whether `provenance` belongs in the
// hashed payload, and the consequence was total: the kernel rejected every
// plan the selector produced and the error said the plan had been tampered
// with. While both implementations were in this module a test imported both
// and compared them directly. This fixture is what replaces that test.
//
// testdata/fingerprint-agreement.json was generated from the two
// implementations while they still agreed. A side whose exclusion set changes
// moves off the frozen value and fails here, at home, without needing the
// other implementation. The kernel's repository holds the mirror of this file.
//
// Regenerating the fixture is a contract change: it must be done where both
// implementations are importable together, which is no longer here, and
// applied in every repository holding a copy.
const fixturePath = "testdata/fingerprint-agreement.json"

type agreementFixture struct {
	Comment             string         `json:"_comment"`
	GeneratedFrom       string         `json:"generated_from"`
	Plan                map[string]any `json:"plan"`
	ExcludedKeysChanged map[string]any `json:"plan_with_excluded_keys_changed"`
	ExpectedFingerprint string         `json:"expected_fingerprint"`
}

// The half of the agreement that survives the split.
//
// This test imports one implementation, so it keeps working when the kernel
// lives in another repository. The kernel's copy is the mirror of this file
// with kernel.DispatchFingerprint substituted; between them the two sides stay
// compatible without either being able to see the other.
//
// A change to this side's exclusion set moves its computed value off the
// frozen one and fails here. That is the whole mechanism: the fixture holds
// the contract, not the other implementation.
func TestTheSelectorMatchesTheFrozenFingerprint(t *testing.T) {
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read %s: %v", fixturePath, err)
	}
	var fixture agreementFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("parse %s: %v", fixturePath, err)
	}
	if fixture.ExpectedFingerprint == "" {
		t.Fatalf("%s carries no expected fingerprint", fixturePath)
	}

	for name, plan := range map[string]map[string]any{
		"plan":                            fixture.Plan,
		"plan_with_excluded_keys_changed": fixture.ExcludedKeysChanged,
	} {
		computed, err := selector.DispatchFingerprint(plan)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if computed != fixture.ExpectedFingerprint {
			t.Errorf("%s: selector computed %s, fixture froze %s.\n"+
				"Either this side's exclusion set changed, or the fixture is stale. "+
				"Both are contract changes and must be made in every repository holding a copy.",
				name, computed, fixture.ExpectedFingerprint)
		}
	}
}
