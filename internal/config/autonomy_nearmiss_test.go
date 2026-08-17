package config

import (
	"strings"
	"testing"
)

// The autonomy overlay's permission values are matched exactly.
//
// agent-autonomy.yaml is narrowing-only: a project overlay may tighten a
// permission but never loosen one. That rule is enforced by ranking the
// overlay's value against the default's, which requires recognising the value
// -- and recognition is an exact map lookup.
//
// So the interesting cases are the near misses: `Allowed`, `allowed `, a value
// that is almost right. Each is refused today. Each is also exactly what a
// later change toward "be forgiving about formatting" would start accepting --
// and being forgiving here means silently admitting a loosening, because the
// value being normalised into existence is `allowed`, the least restrictive
// rank there is.
//
// checkAutonomyOverlay had one unrecognised-value test. These are the ones
// resolve.py called out separately, by name.

func autonomyBase() map[string]any {
	return map[string]any{"dispatch": map[string]any{
		"spawn_child_agent": "allowed_within_selected_scope"}}
}

func autonomyOverlayWith(value any) map[string]any {
	return map[string]any{"dispatch": map[string]any{"spawn_child_agent": value}}
}

func TestAnAlmostRightPermissionValueIsRefused(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		value any
	}{
		{"capitalised", "Allowed"},
		{"upper case", "ALLOWED"},
		{"a trailing space", "allowed "},
		{"a leading space", " allowed"},
		{"a trailing newline", "allowed\n"},
		{"hyphens for underscores", "allowed-within-selected-scope"},
		{"a null", nil},
		{"a list", []any{"never"}},
		{"a number", 10},
		{"a boolean", false},
		{"an empty string", ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := checkAutonomyOverlay(autonomyBase(), autonomyOverlayWith(testCase.value))
			if err == nil {
				t.Fatalf("%#v was accepted as a permission value; normalising it "+
					"into `allowed` would be admitting a loosening", testCase.value)
			}
			if !strings.Contains(err.Error(), "not a recognized") {
				t.Errorf("refused, but not as an unrecognised value: %v", err)
			}
		})
	}
}

func TestRankedValuesNarrowInOneDirectionOnly(t *testing.T) {
	// Not just "loosening to `allowed` is refused" -- the ordering has to hold
	// between the middle ranks too, which is where a rank table with a
	// transposed pair would still pass a test that only ever compared against
	// the endpoints.
	for _, testCase := range []struct {
		name    string
		from    string
		to      string
		allowed bool
	}{
		{"tighter, mid-range", "allowed_within_selected_scope",
			"allowed_with_explicit_read_only_credentials", true},
		{"looser, mid-range", "allowed_with_explicit_read_only_credentials",
			"allowed_within_selected_scope", false},
		{"tighter, to the strictest", "allowed_within_selected_scope", "never", true},
		{"looser, from the strictest", "never", "allowed_within_selected_scope", false},
		{"looser, to the loosest", "allowed_within_selected_scope", "allowed", false},
		{"identical", "allowed_within_selected_scope", "allowed_within_selected_scope", true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			base := map[string]any{"dispatch": map[string]any{
				"spawn_child_agent": testCase.from}}
			err := checkAutonomyOverlay(base, autonomyOverlayWith(testCase.to))
			if testCase.allowed && err != nil {
				t.Errorf("%s -> %s was refused: %v", testCase.from, testCase.to, err)
			}
			if !testCase.allowed && err == nil {
				t.Errorf("%s -> %s was accepted; the overlay may only narrow",
					testCase.from, testCase.to)
			}
			if !testCase.allowed && err != nil && !strings.Contains(err.Error(), "loosen") {
				t.Errorf("refused, but not as a loosening: %v", err)
			}
		})
	}
}

func TestTheRankTableIsStrictlyOrdered(t *testing.T) {
	// Guards the table itself. Two values sharing a rank would make a
	// loosening between them pass the `<` comparison silently, and the cases
	// above only cover the pairs somebody thought to write down.
	seen := map[int]string{}
	for value, rank := range autonomyRestrictivenessRank {
		if other, clash := seen[rank]; clash {
			t.Errorf("%q and %q share rank %d; a change between them would read as "+
				"neither a loosening nor a narrowing", value, other, rank)
		}
		seen[rank] = value
	}
	if len(autonomyRestrictivenessRank) < 3 {
		t.Fatalf("the rank table has %d entries; this test would prove little",
			len(autonomyRestrictivenessRank))
	}
}
