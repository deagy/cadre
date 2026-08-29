package canonicaljson_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/deagy/cadre/cli/internal/kernel"
	"github.com/deagy/cadre/cli/internal/selector"
)

// The frozen form of what agreement_test.go proves live.
//
// agreement_test.go can only exist while both implementations are importable
// from one module. When the kernel moves to its own repository that stops
// being true, and the property it guards -- that `cadre select` and
// `agentic-sdlc validate` compute the same fingerprint over the same plan --
// becomes unprovable in either repository alone.
//
// This fixture is the replacement. It records the plan, a variant differing
// only in the excluded keys, and the one fingerprint both sides produce for
// both. After the split each side tests itself against the frozen value: a
// side that changes its exclusion set moves off the fixture and fails at
// home, without needing to see the other implementation.
//
// It is generated from the two agreeing implementations rather than written
// by hand. A hand-written expectation would record an assumption; this
// records an observation made while both sides were still in the room.
const fixturePath = "testdata/fingerprint-agreement.json"

type agreementFixture struct {
	Comment             string         `json:"_comment"`
	GeneratedFrom       string         `json:"generated_from"`
	Plan                map[string]any `json:"plan"`
	ExcludedKeysChanged map[string]any `json:"plan_with_excluded_keys_changed"`
	ExpectedFingerprint string         `json:"expected_fingerprint"`
}

// excludedKeysChanged returns the plan with only the three keys neither side
// hashes replaced. Its fingerprint must equal the original's; that equality is
// what makes the value a determinism check rather than a checksum over
// environment noise.
func excludedKeysChanged() map[string]any {
	plan := planWithEverything()
	plan["generated_at"] = "2027-01-01T00:00:00+00:00"
	plan["dispatch_fingerprint"] = "sha256:something-else"
	plan["provenance"] = map[string]any{"working_tree": "clean"}
	return plan
}

// TestTheCommittedFixtureMatchesBothImplementations regenerates the fixture
// from live code and fails when the committed copy differs. Set
// UPDATE_FINGERPRINT_FIXTURE=1 to rewrite it -- which is only legitimate while
// both implementations are still importable here.
func TestTheCommittedFixtureMatchesBothImplementations(t *testing.T) {
	plan := planWithEverything()
	variant := excludedKeysChanged()

	fromSelector, err := selector.DispatchFingerprint(plan)
	if err != nil {
		t.Fatalf("selector: %v", err)
	}
	fromKernel, err := kernel.DispatchFingerprint(plan)
	if err != nil {
		t.Fatalf("kernel: %v", err)
	}
	if fromSelector != fromKernel {
		t.Fatalf("the two sides disagree and no fixture may be frozen:\n  selector %s\n  kernel   %s",
			fromSelector, fromKernel)
	}

	variantSelector, err := selector.DispatchFingerprint(variant)
	if err != nil {
		t.Fatalf("selector on variant: %v", err)
	}
	variantKernel, err := kernel.DispatchFingerprint(variant)
	if err != nil {
		t.Fatalf("kernel on variant: %v", err)
	}
	if variantSelector != fromSelector || variantKernel != fromKernel {
		t.Fatalf("changing only the excluded keys changed a fingerprint; "+
			"the fixture would freeze a checksum over metadata\n"+
			"  selector %s -> %s\n  kernel   %s -> %s",
			fromSelector, variantSelector, fromKernel, variantKernel)
	}

	generated := agreementFixture{
		Comment: "Frozen agreement between the selector's and the kernel's dispatch " +
			"fingerprint. Both plans must produce expected_fingerprint on every " +
			"implementation that claims compatibility. Regenerate only where both " +
			"implementations are importable together.",
		GeneratedFrom:       "cadre internal/selector + internal/kernel",
		Plan:                plan,
		ExcludedKeysChanged: variant,
		ExpectedFingerprint: fromSelector,
	}
	encoded, err := json.MarshalIndent(generated, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')

	if os.Getenv("UPDATE_FINGERPRINT_FIXTURE") == "1" {
		if err := os.MkdirAll(filepath.Dir(fixturePath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fixturePath, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (fingerprint %s)", fixturePath, fromSelector)
		return
	}

	committed, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read %s: %v\nRun with UPDATE_FINGERPRINT_FIXTURE=1 to create it.", fixturePath, err)
	}
	if string(committed) != string(encoded) {
		t.Errorf("%s does not match what the implementations produce now.\n"+
			"If an exclusion set changed deliberately, that is a contract change: "+
			"regenerate here and in every repository holding a copy.", fixturePath)
	}
}
