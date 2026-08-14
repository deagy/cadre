package orchestration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func baseRoutingFixture() map[string]any {
	return map[string]any{
		"version": float64(1),
		"routes": []any{
			map[string]any{
				"id":       "backend",
				"keywords": []any{"api", "server"},
				"paths":    []any{"internal/**"},
				"primary":  []any{"backend-engineer"},
			},
		},
		"risk_rules": []any{
			map[string]any{"id": "high-risk", "keywords": []any{"security"}},
		},
		"team_recipes": []any{
			map[string]any{"id": "core-team", "members": []any{"backend-engineer"}},
		},
		"change_intake": map[string]any{
			"keywords": []any{"bug"},
		},
		"cross_stack": map[string]any{
			"route_ids":       []any{"backend"},
			"minimum_matches": float64(2),
		},
		"knowledge_focus": map[string]any{
			"sources": map[string]any{"a": "1"},
		},
		"ignored_gates": []any{"gate-a", "gate-b"},
	}
}

func TestFindRoutingOverlayDiscovery(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	overlayPath := filepath.Join(root, ".agents", "orchestration", "routing-overlay.json")
	writeJSON(t, overlayPath, map[string]any{"version": float64(1)})

	nested := filepath.Join(root, "internal", "cli")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	found, ok := FindRoutingOverlay(nested)
	if !ok {
		t.Fatal("expected to find the overlay")
	}
	if found != overlayPath {
		t.Errorf("found = %q, want %q", found, overlayPath)
	}
}

func TestFindRoutingOverlayNoneFound(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, ok := FindRoutingOverlay(root)
	if ok {
		t.Fatal("expected no overlay to be found")
	}
}

func TestResolveEffectiveRoutingNoOverlay(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "routing.json")
	writeJSON(t, basePath, baseRoutingFixture())

	effective, overlayPath, found, err := ResolveEffectiveRouting(basePath, dir, "")
	if err != nil {
		t.Fatalf("ResolveEffectiveRouting: %v", err)
	}
	if found || overlayPath != "" {
		t.Fatal("expected no overlay found")
	}
	if effective["version"] != float64(1) {
		t.Errorf("effective version = %v", effective["version"])
	}
}

func TestMaterializeEffectiveRoutingNoOverlayIsByteIdentical(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "routing.json")
	original := []byte(`{"version":1,"routes":[]}`)
	if err := os.WriteFile(basePath, original, 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "effective.json")

	_, found, err := MaterializeEffectiveRouting(outPath, basePath, dir, "")
	if err != nil {
		t.Fatalf("MaterializeEffectiveRouting: %v", err)
	}
	if found {
		t.Fatal("expected no overlay found")
	}
	written, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != string(original) {
		t.Errorf("expected byte-identical passthrough, got %q want %q", written, original)
	}
}

func TestMergeRoutingWidensKeywordsAndPaths(t *testing.T) {
	base := baseRoutingFixture()
	overlay := map[string]any{
		"routes": []any{
			map[string]any{
				"id":       "backend",
				"keywords": []any{"api", "server", "http"},
				"paths":    []any{"internal/**", "cmd/**"},
			},
		},
	}

	effective, err := MergeRouting(base, overlay)
	if err != nil {
		t.Fatalf("MergeRouting: %v", err)
	}
	routes := effective["routes"].([]any)
	route := routes[0].(map[string]any)
	keywords := route["keywords"].([]any)
	if len(keywords) != 3 {
		t.Errorf("keywords = %v, want 3 entries", keywords)
	}
	// primary must be untouched (widen patch preserves non-widen fields).
	if route["primary"].([]any)[0] != "backend-engineer" {
		t.Errorf("primary was not preserved: %v", route["primary"])
	}
}

func TestMergeRoutingRejectsNarrowingKeywords(t *testing.T) {
	base := baseRoutingFixture()
	overlay := map[string]any{
		"routes": []any{
			map[string]any{"id": "backend", "keywords": []any{"api"}}, // drops "server"
		},
	}
	_, err := MergeRouting(base, overlay)
	if err == nil {
		t.Fatal("expected an error narrowing keywords")
	}
}

func TestMergeRoutingRejectsChangingNonWidenField(t *testing.T) {
	base := baseRoutingFixture()
	overlay := map[string]any{
		"routes": []any{
			map[string]any{"id": "backend", "primary": []any{"someone-else"}},
		},
	}
	_, err := MergeRouting(base, overlay)
	if err == nil {
		t.Fatal("expected an error changing a non-widen field (primary)")
	}
}

func TestMergeRoutingAddsNewRoute(t *testing.T) {
	base := baseRoutingFixture()
	overlay := map[string]any{
		"routes": []any{
			map[string]any{"id": "frontend", "keywords": []any{"react"}},
		},
	}
	effective, err := MergeRouting(base, overlay)
	if err != nil {
		t.Fatalf("MergeRouting: %v", err)
	}
	routes := effective["routes"].([]any)
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}
}

func TestMergeRoutingRejectsIDCollisionAcrossSections(t *testing.T) {
	base := baseRoutingFixture()
	overlay := map[string]any{
		"routes": []any{
			map[string]any{"id": "core-team", "keywords": []any{"x"}}, // collides with a team_recipes id
		},
	}
	_, err := MergeRouting(base, overlay)
	if err == nil {
		t.Fatal("expected an error for cross-section id collision")
	}
}

func TestMergeRoutingTeamRecipesAdditiveOnly(t *testing.T) {
	base := baseRoutingFixture()
	overlay := map[string]any{
		"team_recipes": []any{
			map[string]any{"id": "new-team", "members": []any{"x"}},
		},
	}
	effective, err := MergeRouting(base, overlay)
	if err != nil {
		t.Fatalf("MergeRouting: %v", err)
	}
	recipes := effective["team_recipes"].([]any)
	if len(recipes) != 2 {
		t.Fatalf("expected 2 team_recipes, got %d", len(recipes))
	}
}

func TestMergeRoutingTeamRecipesRejectsModifyingBase(t *testing.T) {
	base := baseRoutingFixture()
	overlay := map[string]any{
		"team_recipes": []any{
			map[string]any{"id": "core-team", "members": []any{"someone-else"}},
		},
	}
	_, err := MergeRouting(base, overlay)
	if err == nil {
		t.Fatal("expected an error modifying a base team_recipes entry")
	}
}

func TestMergeRoutingChangeIntakeAdditive(t *testing.T) {
	base := baseRoutingFixture()
	overlay := map[string]any{
		"change_intake": map[string]any{"keywords": []any{"bug", "hotfix"}},
	}
	effective, err := MergeRouting(base, overlay)
	if err != nil {
		t.Fatalf("MergeRouting: %v", err)
	}
	ci := effective["change_intake"].(map[string]any)
	if len(ci["keywords"].([]any)) != 2 {
		t.Errorf("change_intake.keywords = %v", ci["keywords"])
	}
}

func TestMergeRoutingChangeIntakeRejectsUnknownField(t *testing.T) {
	base := baseRoutingFixture()
	overlay := map[string]any{
		"change_intake": map[string]any{"bogus": []any{"x"}},
	}
	_, err := MergeRouting(base, overlay)
	if err == nil {
		t.Fatal("expected an error for an unrecognized change_intake field")
	}
}

func TestMergeRoutingCrossStackMinimumMatchesMayDecrease(t *testing.T) {
	base := baseRoutingFixture()
	overlay := map[string]any{
		"cross_stack": map[string]any{"minimum_matches": float64(1)},
	}
	effective, err := MergeRouting(base, overlay)
	if err != nil {
		t.Fatalf("MergeRouting: %v", err)
	}
	cs := effective["cross_stack"].(map[string]any)
	if cs["minimum_matches"] != float64(1) {
		t.Errorf("minimum_matches = %v, want 1", cs["minimum_matches"])
	}
}

func TestMergeRoutingCrossStackMinimumMatchesRejectsIncrease(t *testing.T) {
	base := baseRoutingFixture()
	overlay := map[string]any{
		"cross_stack": map[string]any{"minimum_matches": float64(3)},
	}
	_, err := MergeRouting(base, overlay)
	if err == nil {
		t.Fatal("expected an error increasing minimum_matches")
	}
}

func TestMergeRoutingKnowledgeFocusDeepMerge(t *testing.T) {
	base := baseRoutingFixture()
	overlay := map[string]any{
		"knowledge_focus": map[string]any{"sources": map[string]any{"b": "2"}},
	}
	effective, err := MergeRouting(base, overlay)
	if err != nil {
		t.Fatalf("MergeRouting: %v", err)
	}
	kf := effective["knowledge_focus"].(map[string]any)
	sources := kf["sources"].(map[string]any)
	if sources["a"] != "1" || sources["b"] != "2" {
		t.Errorf("expected both keys after deep merge, got %v", sources)
	}
}

func TestMergeRoutingIgnoredGatesMayShrink(t *testing.T) {
	base := baseRoutingFixture()
	overlay := map[string]any{"ignored_gates": []any{"gate-a"}}
	effective, err := MergeRouting(base, overlay)
	if err != nil {
		t.Fatalf("MergeRouting: %v", err)
	}
	gates := effective["ignored_gates"].([]any)
	if len(gates) != 1 || gates[0] != "gate-a" {
		t.Errorf("ignored_gates = %v", gates)
	}
}

func TestMergeRoutingIgnoredGatesRejectsGrowth(t *testing.T) {
	base := baseRoutingFixture()
	overlay := map[string]any{"ignored_gates": []any{"gate-a", "gate-b", "gate-c"}}
	_, err := MergeRouting(base, overlay)
	if err == nil {
		t.Fatal("expected an error growing ignored_gates")
	}
}

func TestMergeRoutingRejectsVersionChange(t *testing.T) {
	base := baseRoutingFixture()
	overlay := map[string]any{"version": float64(2)}
	_, err := MergeRouting(base, overlay)
	if err == nil {
		t.Fatal("expected an error changing version")
	}
}

func TestMergeRoutingAllowsVersionNoOpRestatement(t *testing.T) {
	base := baseRoutingFixture()
	overlay := map[string]any{"version": float64(1)}
	_, err := MergeRouting(base, overlay)
	if err != nil {
		t.Fatalf("expected no error restating the same version, got: %v", err)
	}
}

func TestMergeRoutingRejectsUnknownTopLevelField(t *testing.T) {
	base := baseRoutingFixture()
	overlay := map[string]any{"bogus_field": "x"}
	_, err := MergeRouting(base, overlay)
	if err == nil {
		t.Fatal("expected an error for an unrecognized top-level field")
	}
}

func TestResolveEffectiveRoutingWithOverlayEndToEnd(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	basePath := filepath.Join(dir, "routing.json")
	writeJSON(t, basePath, baseRoutingFixture())

	overlayPath := filepath.Join(dir, ".agents", "orchestration", "routing-overlay.json")
	writeJSON(t, overlayPath, map[string]any{
		"routes": []any{
			map[string]any{"id": "new-route", "keywords": []any{"x"}},
		},
	})

	effective, resolvedPath, found, err := ResolveEffectiveRouting(basePath, dir, "")
	if err != nil {
		t.Fatalf("ResolveEffectiveRouting: %v", err)
	}
	if !found {
		t.Fatal("expected overlay to be found")
	}
	if resolvedPath != overlayPath {
		t.Errorf("resolvedPath = %q, want %q", resolvedPath, overlayPath)
	}
	routes := effective["routes"].([]any)
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes after merge, got %d", len(routes))
	}
}

func TestEffectiveRoutingConfigConvertsToTypedStruct(t *testing.T) {
	effective := baseRoutingFixture()
	config, err := EffectiveRoutingConfig(effective)
	if err != nil {
		t.Fatalf("EffectiveRoutingConfig: %v", err)
	}
	if len(config.Routes) != 1 || config.Routes[0].ID != "backend" {
		t.Errorf("Routes = %+v", config.Routes)
	}
}
