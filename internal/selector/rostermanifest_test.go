package selector

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The roster manifest, and the layout the platform must stop assuming.
//
// The selector resolved a roster's catalog and routing by joining fixed
// subpaths: <roster>/catalog.yaml and <roster>/orchestration/routing.json.
// That works for exactly one roster -- this repository's own, whose manifest
// declares those very paths -- and fails for every package that lays itself
// out differently, which is the entire reason a manifest exists.
//
// Verified against the Python selector, which reads the manifest: a package
// declaring definitions/agents.yaml and rules/routes.json produced a plan
// there and "no such file" here.
//
// Ported from roster/orchestration/src/roster_manifest.py, with the refusals
// checked against roster/orchestration/test/test_roster_package.py's cases.

// writeManifestPackage builds a roster package on disk and returns its root.
// The overrides are applied to a valid manifest, so each case differs from a
// working package in exactly one way.
func writeManifestPackage(t *testing.T, overrides map[string]any) string {
	t.Helper()
	root := t.TempDir()
	for _, directory := range []string{"definitions", "rules", "roles", "policy"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{
		filepath.Join("definitions", "agents.yaml"),
		filepath.Join("rules", "routes.json"),
	} {
		if err := os.WriteFile(filepath.Join(root, file), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := map[string]any{
		"schema_version": 1, "id": "widget-works", "version": "0.1.0",
		"catalog": "definitions/agents.yaml", "routing": "rules/routes.json",
		"role_root": "roles", "shared_policy_root": "policy",
	}
	for key, value := range overrides {
		if value == nil {
			delete(manifest, key)
			continue
		}
		manifest[key] = value
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, RosterManifestFilename), encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestTheManifestDecidesWhereTheCatalogAndRoutingLive(t *testing.T) {
	// The defect. Nothing here is at catalog.yaml or orchestration/routing.json,
	// so a loader that joins those paths finds neither.
	root := writeManifestPackage(t, nil)
	manifest, err := LoadRosterManifest(root)
	if err != nil {
		t.Fatalf("a valid roster package was refused: %v", err)
	}
	if got, want := manifest.Catalog, filepath.Join(root, "definitions", "agents.yaml"); got != want {
		t.Errorf("catalog = %s, want the declared path %s", got, want)
	}
	if got, want := manifest.Routing, filepath.Join(root, "rules", "routes.json"); got != want {
		t.Errorf("routing = %s, want the declared path %s", got, want)
	}
	if manifest.ID != "widget-works" || manifest.Version != "0.1.0" {
		t.Errorf("identity = %s/%s", manifest.ID, manifest.Version)
	}
}

func TestAMissingManifestIsRefusedRatherThanDefaulted(t *testing.T) {
	// The failure this refusal prevents is quiet. Falling back to this
	// repository's layout means a directory that is not a roster package --
	// a mistyped path, a partial checkout -- resolves against whatever
	// catalog.yaml happens to be there, or none, and the caller who asked for
	// one roster gets a plan built from another.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "catalog.yaml"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadRosterManifest(root)
	if err == nil {
		t.Fatal("a directory with no manifest was accepted as a roster package")
	}
	if !strings.Contains(err.Error(), RosterManifestFilename) {
		t.Errorf("the refusal does not name what is missing: %v", err)
	}
}

func TestAManifestPathMayNotEscapeItsPackage(t *testing.T) {
	// A roster package is a unit a project may fetch from elsewhere. A
	// manifest that can name a path outside itself can point the selector at
	// any readable file on the machine -- and the resulting plan would look
	// entirely ordinary.
	for _, probe := range []struct{ name, value string }{
		{"a parent traversal", "../../../etc/passwd"},
		{"an absolute path", "/etc/passwd"},
		{"a traversal that returns", "roles/../../outside.yaml"},
	} {
		t.Run(probe.name, func(t *testing.T) {
			_, err := LoadRosterManifest(writeManifestPackage(t,
				map[string]any{"catalog": probe.value}))
			if err == nil {
				t.Fatalf("a manifest naming %q was accepted", probe.value)
			}
			if !strings.Contains(err.Error(), "escapes its manifest directory") {
				t.Errorf("refused for a different reason: %v", err)
			}
		})
	}

	// And a path that stays inside is not refused merely for containing "..".
	root := writeManifestPackage(t, map[string]any{"catalog": "roles/../definitions/agents.yaml"})
	if _, err := LoadRosterManifest(root); err != nil {
		t.Errorf("a path that traverses but stays inside was refused: %v", err)
	}
}

func TestAnUnusableManifestIsRefusedByName(t *testing.T) {
	// Each of these otherwise surfaces later as a confusing failure about a
	// file, rather than about the manifest that named it.
	for _, probe := range []struct {
		name      string
		overrides map[string]any
		wants     string
	}{
		{"a missing required field", map[string]any{"version": nil}, "missing required field(s): version"},
		{"several missing fields at once", map[string]any{"id": nil, "role_root": nil},
			"missing required field(s)"},
		{"an unsupported schema version", map[string]any{"schema_version": 99},
			"unsupported schema_version 99"},
		{"a catalog that does not exist", map[string]any{"catalog": "definitions/absent.yaml"},
			"names a file that does not exist"},
		{"a role root that does not exist", map[string]any{"role_root": "absent"},
			"names a directory that does not exist"},
		{"a role root that is a file", map[string]any{"role_root": "definitions/agents.yaml"},
			"names a directory that does not exist"},
		{"a catalog that is a directory", map[string]any{"catalog": "definitions"},
			"names a file that does not exist"},
		{"an id that is not a string", map[string]any{"id": 7}, "must be a non-empty string"},
		{"an empty declared path", map[string]any{"routing": ""},
			"must be a non-empty relative path"},
	} {
		t.Run(probe.name, func(t *testing.T) {
			_, err := LoadRosterManifest(writeManifestPackage(t, probe.overrides))
			if err == nil {
				t.Fatal("an unusable manifest was accepted")
			}
			if !strings.Contains(err.Error(), probe.wants) {
				t.Errorf("refused for a different reason than this case is about.\n"+
					"wanted something naming %q, got: %v", probe.wants, err)
			}
		})
	}

	t.Run("a manifest that is not JSON", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, RosterManifestFilename),
			[]byte("{not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := LoadRosterManifest(root)
		if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
			t.Errorf("a malformed manifest was not refused as such: %v", err)
		}
	})
}

func TestThisRepositorysOwnRosterIsAValidPackage(t *testing.T) {
	// The check that keeps the change above honest. Cadre's own roster carries
	// a manifest declaring catalog.yaml and orchestration/routing.json -- the
	// paths that used to be assumed -- so reading the manifest changes nothing
	// here. If it ever stops being a valid package, every selection in this
	// repository stops working, and that should fail loudly in one place
	// rather than everywhere at once.
	root := filepath.Join(selectorRepoRoot(t), "roster")
	manifest, err := LoadRosterManifest(root)
	if err != nil {
		t.Fatalf("this repository's own roster is not a usable package: %v", err)
	}
	if got, want := manifest.Catalog, filepath.Join(root, "catalog.yaml"); got != want {
		t.Errorf("catalog = %s, want %s", got, want)
	}
	if got, want := manifest.Routing,
		filepath.Join(root, "orchestration", "routing.json"); got != want {
		t.Errorf("routing = %s, want %s", got, want)
	}
}
