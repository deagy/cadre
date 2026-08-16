package selector

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Resolving an overlay, and what the merged result is refused for.
//
// overlay_test.go covers MergeRouting -- the per-section merge rules -- in
// depth. This covers the two functions around it, which were the entry points
// nothing reached: LoadOverlay at 0% and ResolveEffectiveRouting at 18.2%,
// with ValidateRoutingConfig's ten refusals at 40.3%.
//
// These matter because an overlay is the one part of routing a *consuming*
// project writes. Everything else here is this repository's own file, reviewed
// in this repository. An overlay arrives from outside, and the refusals below
// are the only thing between a typo in it and a selector that silently routes
// to nobody, or routes two different rules under one id.

func minimalRouting() map[string]any {
	return map[string]any{
		"version":    float64(1),
		"routes":     []any{},
		"risk_rules": []any{},
	}
}

func writeOverlay(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "routing-overlay.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWithNoOverlayTheBaseConfigurationIsReturnedUnvalidated(t *testing.T) {
	// The asymmetry ResolveEffectiveRouting's comment calls deliberate, and
	// which nothing exercised: with no overlay the base is returned as parsed
	// and is NOT revalidated.
	//
	// It is not an oversight to preserve for its own sake -- it is what keeps
	// a selection run from failing on a problem no overlay introduced. The
	// base file is this repository's own and is guarded by its own tests;
	// revalidating it on every `cadre select` would turn a repository-level
	// defect into a per-invocation failure for every consumer.
	base := map[string]any{"version": float64(99), "routes": "not-a-list"}
	resolved, err := ResolveEffectiveRouting(base, "")
	if err != nil {
		t.Fatalf("the no-overlay path validated the base and refused it: %v", err)
	}
	if len(resolved) != len(base) || resolved["version"] != base["version"] {
		t.Errorf("the base was not returned as parsed: %v", resolved)
	}
	// And the same document *is* refused once an overlay puts it through the
	// merge path, so the exemption is scoped to "no overlay" rather than being
	// a validator that never runs.
	if _, err := ResolveEffectiveRouting(base, writeOverlay(t, `{}`)); err == nil {
		t.Error("the merged path accepted a configuration the validator rejects")
	}
}

func TestAnOverlayThatCannotBeReadIsRefusedByName(t *testing.T) {
	// Each of these otherwise ends as a nil map flowing into the merge, where
	// the symptom is an empty effective configuration -- a selector that
	// matches nothing, reported as "needs-triage" rather than as a broken
	// overlay.
	for _, probe := range []struct{ name, content, wants string }{
		{"malformed JSON", `{"routes": [`, "malformed overlay JSON"},
		{"a JSON array at the root", `[]`, "root must be a JSON object"},
		{"a bare string at the root", `"routes"`, "root must be a JSON object"},
		{"a JSON null", `null`, "root must be a JSON object"},
	} {
		t.Run(probe.name, func(t *testing.T) {
			_, err := LoadOverlay(writeOverlay(t, probe.content))
			if err == nil {
				t.Fatalf("accepted an overlay that is not one: %s", probe.content)
			}
			if !strings.Contains(err.Error(), probe.wants) {
				t.Errorf("refused for a different reason than this case is about.\n"+
					"wanted something naming %q, got: %v", probe.wants, err)
			}
		})
	}

	t.Run("an overlay that is not there", func(t *testing.T) {
		// Distinct from the above: --routing-overlay naming a path that does
		// not exist is a typo in an invocation, and must not be read as "no
		// overlay was asked for".
		missing := filepath.Join(t.TempDir(), "absent.json")
		if _, err := LoadOverlay(missing); err == nil {
			t.Fatal("a missing overlay file was accepted")
		}
		if _, err := ResolveEffectiveRouting(minimalRouting(), missing); err == nil {
			t.Error("resolving treated a missing overlay as no overlay")
		}
	})
}

func TestAnOverlayIsMergedAndThenValidated(t *testing.T) {
	// The happy path, and the ordering the comment describes: validation runs
	// on the merged result, not on the way in. An overlay can be individually
	// well-formed and still produce an effective configuration that is not.
	base := map[string]any{
		"version":    float64(1),
		"routes":     []any{map[string]any{"id": "existing", "keywords": []any{"api"}}},
		"risk_rules": []any{},
	}
	overlay := writeOverlay(t, `{"routes": [{"id": "added", "keywords": ["frontend"]}]}`)
	effective, err := ResolveEffectiveRouting(base, overlay)
	if err != nil {
		t.Fatalf("a well-formed overlay was refused: %v", err)
	}
	routes := objectList(effective["routes"])
	if len(routes) != 2 {
		t.Fatalf("the overlay's route was not merged in: %d routes", len(routes))
	}
	// And the base is not modified in place -- a consuming project resolving
	// twice in one process must get the same answer both times.
	if got := len(objectList(base["routes"])); got != 1 {
		t.Errorf("resolving mutated the base configuration: %d routes", got)
	}

	// An overlay whose own shape is fine but whose *result* is not.
	clashing := writeOverlay(t, `{"routes": [{"id": "existing", "keywords": ["other"]}],
		"risk_rules": [{"id": "existing", "keywords": ["risk"]}]}`)
	if _, err := ResolveEffectiveRouting(base, clashing); err == nil {
		t.Error("an overlay producing a duplicate id across sections was accepted")
	}
}

func TestTheEffectiveConfigurationIsRefusedForEachDistinctReason(t *testing.T) {
	// One case per refusal. Each is a way an overlay silently changes what the
	// selector does rather than failing, so the message has to name the thing
	// that is wrong -- an overlay author is not reading this code.
	for _, probe := range []struct {
		name  string
		build func() map[string]any
		wants string
	}{
		{"a version other than 1", func() map[string]any {
			config := minimalRouting()
			config["version"] = float64(2)
			return config
		}, "version 1"},
		{"routes that are not a list", func() map[string]any {
			config := minimalRouting()
			config["routes"] = map[string]any{"id": "not-a-list"}
			return config
		}, "version 1"},
		{"risk_rules missing entirely", func() map[string]any {
			config := minimalRouting()
			delete(config, "risk_rules")
			return config
		}, "version 1"},
		{"an id shared by a route and a risk rule", func() map[string]any {
			config := minimalRouting()
			config["routes"] = []any{map[string]any{"id": "shared"}}
			config["risk_rules"] = []any{map[string]any{"id": "shared"}}
			return config
		}, "must be unique"},
		{"keyword_groups that are not a list", func() map[string]any {
			config := minimalRouting()
			config["routes"] = []any{map[string]any{"id": "r", "keyword_groups": "deploy"}}
			return config
		}, "keyword_groups"},
		{"an empty keyword group", func() map[string]any {
			config := minimalRouting()
			config["routes"] = []any{map[string]any{"id": "r", "keyword_groups": []any{[]any{}}}}
			return config
		}, "keyword_groups"},
		{"an empty string inside a keyword group", func() map[string]any {
			config := minimalRouting()
			config["routes"] = []any{map[string]any{"id": "r",
				"keyword_groups": []any{[]any{"deploy", ""}}}}
			return config
		}, "keyword_groups"},
		{"context_packs that are not a list", func() map[string]any {
			config := minimalRouting()
			config["context_packs"] = "pack"
			return config
		}, "context_packs must be a list"},
		{"a context pack with no id", func() map[string]any {
			config := minimalRouting()
			config["context_packs"] = []any{map[string]any{"definition": "d"}}
			return config
		}, "non-empty id and definition"},
		{"two context packs with one id", func() map[string]any {
			config := minimalRouting()
			config["context_packs"] = []any{
				map[string]any{"id": "p", "definition": "d", "version": float64(1)},
				map[string]any{"id": "p", "definition": "e", "version": float64(1)},
			}
			return config
		}, "duplicate context pack id"},
		// The instance range is checked only for `type: dynamic` recipes -- a
		// fixed one lists its members explicitly and has no range to satisfy.
		{"a dynamic team recipe whose instance range is inverted", func() map[string]any {
			config := minimalRouting()
			config["team_recipes"] = []any{map[string]any{
				"id": "recipe", "type": "dynamic", "instances": map[string]any{
					"min": float64(3), "max": float64(1),
				}}}
			return config
		}, "1 <= min <= max"},
		{"a dynamic team recipe asking for zero instances", func() map[string]any {
			config := minimalRouting()
			config["team_recipes"] = []any{map[string]any{
				"id": "recipe", "type": "dynamic", "instances": map[string]any{
					"min": float64(0), "max": float64(4),
				}}}
			return config
		}, "1 <= min <= max"},
		{"a dynamic team recipe whose bounds are booleans", func() map[string]any {
			// `true` is not an integer, but a reader that only asks "is this a
			// number?" of a JSON document can be talked into treating it as 1.
			config := minimalRouting()
			config["team_recipes"] = []any{map[string]any{
				"id": "recipe", "type": "dynamic", "instances": map[string]any{
					"min": true, "max": true,
				}}}
			return config
		}, "1 <= min <= max"},
	} {
		t.Run(probe.name, func(t *testing.T) {
			if err := ValidateRoutingConfig(minimalRouting()); err != nil {
				t.Fatalf("the minimal configuration is itself invalid, so this "+
					"case would pass for the wrong reason: %v", err)
			}
			err := ValidateRoutingConfig(probe.build())
			if err == nil {
				t.Fatal("accepted a configuration the selector cannot use")
			}
			if !strings.Contains(err.Error(), probe.wants) {
				t.Errorf("refused for a different reason than this case is about.\n"+
					"wanted something naming %q, got: %v", probe.wants, err)
			}
		})
	}
}

func TestARefusalNamesTheRuleItCameFrom(t *testing.T) {
	// A message saying only "keyword_groups must contain non-empty string
	// groups" sends an overlay author looking through every rule they wrote.
	err := ValidateRoutingConfig(map[string]any{
		"version": float64(1), "risk_rules": []any{},
		"routes": []any{
			map[string]any{"id": "fine", "keyword_groups": []any{[]any{"deploy"}}},
			map[string]any{"id": "the-broken-one", "keyword_groups": []any{[]any{""}}},
		},
	})
	if err == nil {
		t.Fatal("accepted an empty keyword")
	}
	if !strings.Contains(err.Error(), "the-broken-one") {
		t.Errorf("the refusal does not name the offending rule: %v", err)
	}
	if strings.Contains(err.Error(), "fine") {
		t.Errorf("the refusal names a rule that is not the problem: %v", err)
	}
}
