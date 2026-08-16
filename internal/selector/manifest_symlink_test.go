package selector

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A roster package's declared paths stay inside the package, symlinks included.
//
// The containment check in rosterResource exists so that a foreign roster
// package -- one this repository did not write, resolved from a manifest --
// cannot reach outside its own directory. `..` and an absolute path are caught
// because the escape is written in the manifest text.
//
// A symlink is not written anywhere. `"catalog": "catalog.yaml"` reads as
// plainly contained, and if that name is a symlink it can point at any file on
// the machine. The lexical check passed it, os.Stat followed it, and the
// selector loaded a catalog from outside the package it was told to use.
//
// roster_manifest.py resolved the candidate before comparing, which is why its
// own suite has a symlink case. The port compared unresolved paths.

// rosterPackage writes a manifest whose every field is valid, so a case that
// injects one defect is refused for that defect and not something else.
//
// Built the long way after three probes were refused for the wrong reason --
// a wrong filename, then a missing field, then another. A refusal that arrives
// before the check under test proves nothing about it.
func rosterPackage(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, directory := range []string{"roles", "shared"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("catalog.yaml", "version: 1\nagents: {}\n")
	write("routing.json", `{"version": 1, "routes": []}`)
	write(RosterManifestFilename, `{"schema_version": 1, "id": "fixture",`+
		`"version": "1.0.0", "catalog": "catalog.yaml", "routing": "routing.json",`+
		`"role_root": "roles", "shared_policy_root": "shared"}`)
	return root
}

func TestTheValidFixtureLoads(t *testing.T) {
	// The baseline every case below depends on. Without it, a fixture broken in
	// some unrelated way would make each of them "pass" on the wrong refusal.
	if _, err := LoadRosterManifest(rosterPackage(t)); err != nil {
		t.Fatalf("the fixture package is not valid, so no case here proves anything: %v", err)
	}
}

func TestADeclaredPathMayNotBeASymlinkOutOfThePackage(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		field     string
		directory bool
	}{
		{"a catalog symlinked to a file outside", "catalog.yaml", false},
		{"a role root symlinked to a directory outside", "roles", true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			outside := t.TempDir()
			target := filepath.Join(outside, "target")
			if testCase.directory {
				if err := os.MkdirAll(target, 0o755); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(target, []byte("version: 1\nagents: {}\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			root := rosterPackage(t)
			inside := filepath.Join(root, testCase.field)
			if err := os.RemoveAll(inside); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, inside); err != nil {
				t.Skipf("symlinks unavailable here: %v", err)
			}

			_, err := LoadRosterManifest(root)
			if err == nil {
				t.Fatalf("%s escaped the package and was accepted", testCase.field)
			}
			if !strings.Contains(err.Error(), "escapes its manifest directory") {
				t.Errorf("refused, but not as an escape -- so the containment check "+
					"is not what caught it: %v", err)
			}
		})
	}
}

func TestASymlinkToSomewhereMissingOutsideIsStillAnEscape(t *testing.T) {
	// The case a strict resolver misses. filepath.EvalSymlinks refuses a path
	// unless all of it exists, so "link/roles" where link escapes and roles is
	// absent resolves to nothing -- and the lexical path, which looks
	// contained, is what would get compared.
	//
	// The escape is in the link, not in what it points at, so a missing target
	// makes it no less an escape.
	outside := t.TempDir()
	root := rosterPackage(t)
	if err := os.RemoveAll(filepath.Join(root, "roles")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "nothing-here"),
		filepath.Join(root, "roles")); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}

	_, err := LoadRosterManifest(root)
	if err == nil {
		t.Fatal("a dangling symlink out of the package was accepted")
	}
	if !strings.Contains(err.Error(), "escapes its manifest directory") {
		t.Errorf("refused as a missing directory rather than as an escape. That "+
			"reads as a typo in the manifest, when the manifest is pointing out "+
			"of the package: %v", err)
	}
}

func TestASymlinkStayingInsideThePackageIsAccepted(t *testing.T) {
	// The other half, and the one that makes the check containment rather than
	// a ban on symlinks. A package that arranges its own files behind links is
	// doing nothing wrong, and refusing it would push people to stop declaring
	// paths honestly.
	root := rosterPackage(t)
	if err := os.MkdirAll(filepath.Join(root, "actual-roles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "roles")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "actual-roles"),
		filepath.Join(root, "roles")); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}

	manifest, err := LoadRosterManifest(root)
	if err != nil {
		t.Fatalf("a symlink resolving inside the package was refused: %v", err)
	}
	// And it resolves to the target, as pathlib's resolve() does -- so a
	// caller comparing paths sees one name for the directory, not two.
	if filepath.Base(manifest.RoleRoot) != "actual-roles" {
		t.Errorf("role_root resolved to %s, not through the link to actual-roles",
			manifest.RoleRoot)
	}
}

func TestAPackageUnderASymlinkedParentIsNotItselfAnEscape(t *testing.T) {
	// The failure the fix could have introduced. Resolving the candidate while
	// leaving the root unresolved makes every path in a package that sits under
	// a symlinked parent look like it escapes -- and on macOS that is any
	// package under /tmp, which is a link to /private/tmp.
	//
	// Both sides have to be resolved, and this is what says so.
	actual := rosterPackage(t)
	linkParent := t.TempDir()
	link := filepath.Join(linkParent, "package-link")
	if err := os.Symlink(actual, link); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}

	if _, err := LoadRosterManifest(link); err != nil {
		t.Errorf("a package reached through a symlinked parent was refused; "+
			"its own files did not move: %v", err)
	}
}
