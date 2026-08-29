package canonicaljson_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/deagy/cadre/cli/internal/selector"
)

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
