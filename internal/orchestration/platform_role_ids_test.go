package orchestration

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Platform code must not name a specific roster's roles.
//
// Cadre's own catalog is one roster. The dispatch and selection code is
// supposed to work for any of them, so a literal like
// FALLBACK_REVIEWER = "code-reviewer" is a bug that only shows up for someone
// else: a foreign roster has no such role, and selection raises "unknown
// agent" rather than degrading.
//
// Ported from test_roster_boundary.py's TestNoCadreRoleIdsInPlatformCode,
// whose PLATFORM_MODULES list named mcp/dispatch_core.py and
// mcp/dispatch_server.py. Those files are gone; the dispatch surface they
// covered is this package.
//
// That guard carries a lesson worth keeping with it. Its first draft checked
// only the module where the known defect lived, and planting a hardcoded role
// id in dispatch_core.py -- the file five revisions of the requirements
// baseline forgot existed -- passed every test. The failure mode of a guard
// like this is an incomplete list, not an empty one, which is why the
// membership check below exists alongside the scan.

// platformGoModules are the files that decide *which* agent runs.
//
// Two exclusions, both decisions rather than oversights:
//
//   - Generators format catalog content, so naming a role is their job rather
//     than a leak. internal/generators names product-owner-aide to reproduce a
//     hand-authored comment block byte-for-byte, and the Python generator it
//     was ported from does the same.
//   - internal/kernel is a separate component with its own ownership boundary,
//     and it names release-engineer: `agentic-sdlc plan` adds that role to
//     support for a production-release workflow. That predates the Go port --
//     the deleted Python kernel carried the identical line -- and
//     test_roster_boundary.py's PLATFORM_MODULES never covered the kernel
//     either. Scanning it here would fail on faithful behaviour and would be
//     a change to plan output disguised as a test.
//
// internal/cli is included. It was not, and that is the gap this list existed
// to prevent: `cadre select`'s flag handling now resolves the roster root, the
// knowledge CLI path and every default the plan is built from, so a role id
// hardcoded as a fallback there leaks exactly as one in the selector would.
func platformGoModules(t *testing.T) []string {
	t.Helper()
	var modules []string
	for _, directory := range []string{".", "../selector", "../cli"} {
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatalf("cannot read %s: %v", directory, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			modules = append(modules, filepath.Join(directory, name))
		}
	}
	return modules
}

func catalogRoleIDs(t *testing.T) []string {
	t.Helper()
	content, err := os.ReadFile("../../roster/catalog.yaml")
	if err != nil {
		t.Skipf("not running inside a source checkout: %v", err)
	}
	var catalog struct {
		Agents map[string]any `yaml:"agents"`
	}
	if err := yaml.Unmarshal(content, &catalog); err != nil {
		t.Fatalf("cannot parse catalog: %v", err)
	}
	ids := make([]string, 0, len(catalog.Agents))
	for id := range catalog.Agents {
		ids = append(ids, id)
	}
	return ids
}

func TestTheRoleIDGuardIsNotVacuous(t *testing.T) {
	// An empty role set would make the scan below pass against anything, and
	// an empty module list would make it scan nothing. Both are how this
	// guard fails silently.
	ids := catalogRoleIDs(t)
	if len(ids) < 100 {
		t.Fatalf("only %d catalog role ids loaded; the scan would be near-vacuous", len(ids))
	}
	found := false
	for _, id := range ids {
		if id == "code-reviewer" {
			found = true
		}
	}
	if !found {
		t.Error("the catalog loaded without code-reviewer, so it is not the real one")
	}

	modules := platformGoModules(t)
	if len(modules) < 10 {
		t.Fatalf("only %d platform modules found; the list is incomplete", len(modules))
	}
	// The dispatch surface specifically, because that is the one a previous
	// version of this guard omitted.
	for _, required := range []string{
		"dispatch_core.go", "dispatch_core_phase2.go", "dispatch_core_phase1.go",
		// The selection surface, in both the package that computes a plan and
		// the one that decides what to compute it from.
		"plan.go", "select_go.go",
	} {
		present := false
		for _, module := range modules {
			if filepath.Base(module) == required {
				present = true
			}
		}
		if !present {
			t.Errorf("%s is not in the scanned platform modules; a guard that omits the "+
				"dispatch surface covers only what everyone already remembers", required)
		}
	}
}

func TestNoPlatformModuleHardcodesACadreRoleID(t *testing.T) {
	ids := catalogRoleIDs(t)
	patterns := make([]*regexp.Regexp, 0, len(ids))
	for _, id := range ids {
		patterns = append(patterns, regexp.MustCompile(`"`+regexp.QuoteMeta(id)+`"`))
	}

	for _, module := range platformGoModules(t) {
		content, err := os.ReadFile(module)
		if err != nil {
			t.Fatalf("cannot read %s: %v", module, err)
		}
		for lineNumber, line := range strings.Split(string(content), "\n") {
			trimmed := strings.TrimSpace(line)
			// A comment may name a role to explain something. Code may not.
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			for index, pattern := range patterns {
				if pattern.MatchString(line) {
					t.Errorf("%s:%d hardcodes the Cadre role id %q: %s\n"+
						"Platform code must not name a specific roster's roles -- a foreign "+
						"roster has no such role. Move it into roster-declared data.",
						module, lineNumber+1, ids[index], trimmed)
				}
			}
		}
	}
}
