package generators

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The committed catalog and the roles on disk, checked against each other.
//
// `roster/catalog.yaml` is the inventory the whole selector reads: routing,
// dispatch and the packaged plugin all take their idea of what roles exist
// from it. A role added on disk and not declared there is invisible to every
// one of them, and a declaration whose file was deleted or moved is a route to
// nowhere that only fails when somebody is dispatched down it.
//
// Ported from roster/orchestration/test/test_repository_health.py, which took
// its `is_role_definition` predicate by importing the *Python* generator --
// the one the Go CLI replaced. That import is why three otherwise-dead Python
// generators cannot be deleted, and it also meant the two implementations of
// this predicate sat side by side with nothing asserting they agreed.
//
// This is the stronger half of what was there: the Go tests already discovered
// roles, but only asserted "at least a hundred, and two named ones exist".

// catalogDefinitions reads the definition path each role declares.
//
// Parsed with the same two-space/four-space indentation rule the Python guard
// used rather than a YAML library: this file is generated and its shape is
// fixed, and a parser that accepted more than the generator emits would let a
// malformed catalog pass.
func catalogDefinitions(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the catalog: %v", err)
	}
	definitions := map[string]string{}
	current := ""
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") &&
			strings.HasSuffix(strings.TrimRight(line, " \t"), ":"):
			current = strings.TrimSuffix(strings.TrimSpace(line), ":")
		case current != "" && strings.HasPrefix(strings.TrimSpace(line), "definition:"):
			_, value, _ := strings.Cut(strings.TrimSpace(line), ":")
			definitions[current] = strings.TrimSpace(value)
		}
	}
	return definitions
}

// rolesOnDisk is every AGENT.md this roster claims as its own.
func rolesOnDisk(t *testing.T, rosterRoot string) []string {
	t.Helper()
	var found []string
	err := filepath.Walk(rosterRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || info.Name() != "AGENT.md" {
			return nil
		}
		relative, err := filepath.Rel(rosterRoot, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		// The exclusion that matters: roster/orchestration/test/fixtures/
		// holds AGENT.md files describing some *other* roster, and a walk
		// without this silently claims them as this one's.
		if isRoleDefinition(relative) {
			found = append(found, relative)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the roster: %v", err)
	}
	sort.Strings(found)
	return found
}

func TestTheCatalogDeclaresExactlyTheRolesOnDisk(t *testing.T) {
	rosterRoot := filepath.Join("..", "..", "roster")
	if _, err := os.Stat(rosterRoot); err != nil {
		t.Fatalf("not running inside a source checkout: %v", err)
	}

	declared := catalogDefinitions(t, filepath.Join(rosterRoot, "catalog.yaml"))
	if len(declared) == 0 {
		t.Fatal("the catalog declares no roles at all; this test would prove nothing")
	}
	onDisk := rolesOnDisk(t, rosterRoot)

	declaredPaths := map[string]string{}
	for role, definition := range declared {
		declaredPaths[definition] = role
	}

	for _, path := range onDisk {
		if _, present := declaredPaths[path]; !present {
			t.Errorf("%s exists on disk and the catalog does not declare it -- "+
				"routing, dispatch and the packaged plugin will not see this role", path)
		}
	}
	present := map[string]bool{}
	for _, path := range onDisk {
		present[path] = true
	}
	for definition, role := range declaredPaths {
		if !present[definition] {
			t.Errorf("the catalog declares %s at %s and no such file exists -- "+
				"a route to nowhere that fails only when somebody is dispatched down it",
				role, definition)
		}
	}
	if len(declared) != len(onDisk) {
		t.Errorf("the catalog declares %d roles and %d are on disk",
			len(declared), len(onDisk))
	}
}

func TestRoleDiscoveryFindsTheSameRolesTheCatalogDeclares(t *testing.T) {
	// DiscoverRoles is what the generator builds the catalog *from*, so this
	// closes the loop: what it finds, what the committed catalog says, and
	// what is on disk are all one set.
	rosterRoot := filepath.Join("..", "..", "roster")
	if _, err := os.Stat(rosterRoot); err != nil {
		t.Fatalf("not running inside a source checkout: %v", err)
	}

	discovered, err := DiscoverRoles(rosterRoot)
	if err != nil {
		// Fails rather than skips. A discovery that cannot run is not a
		// reason to report success -- the Go test this replaces skipped here,
		// and a skip is indistinguishable from a pass in a summary.
		t.Fatalf("discovering roles: %v", err)
	}
	declared := catalogDefinitions(t, filepath.Join(rosterRoot, "catalog.yaml"))

	for role := range declared {
		if _, found := discovered[role]; !found {
			t.Errorf("the catalog declares %s and discovery does not find it", role)
		}
	}
	for role := range discovered {
		if _, found := declared[role]; !found {
			t.Errorf("discovery finds %s and the catalog does not declare it", role)
		}
	}
}

func TestARosterFixtureIsNeverClaimedAsARole(t *testing.T) {
	// The predicate this guard rests on, stated directly. The selector's own
	// test fixtures are AGENT.md files describing a different roster; counting
	// them would inflate the inventory and put roles nobody wrote into the
	// packaged plugin.
	for _, probe := range []struct {
		path  string
		isOwn bool
	}{
		{"planning/product-intent-agent/AGENT.md", true},
		{"engineering/backend-engineer/AGENT.md", true},
		{"orchestration/test/fixtures/alpha/AGENT.md", false},
		{"orchestration/test/AGENT.md", false},
		{"something/fixtures/AGENT.md", false},
	} {
		if got := isRoleDefinition(probe.path); got != probe.isOwn {
			t.Errorf("%s: claimed=%v, wanted %v", probe.path, got, probe.isOwn)
		}
	}
}
