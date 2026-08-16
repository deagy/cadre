package selector

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// There is one routing-overlay implementation.
//
// There were two. internal/selector/overlay.go is the one `cadre select`
// calls; internal/orchestration/routing_overlay.go was a second port of the
// same Python module, and nothing ever called it -- not another package, not
// its own. It carried the full merge ruleset and 23 tests, all exercising code
// no invocation reached.
//
// That is worse than dead weight. The rules it encoded were the rules that
// decide whether a consuming project may narrow its own review requirements,
// and a reader finding it has no way to tell which copy is authoritative. A
// fix applied to the wrong one looks correct, tests green, and changes
// nothing.
//
// So the count is the property.

// overlayEntryPoints names the functions specific to *this* overlay -- the one
// that merges a project's routing-overlay document into routing.json.
//
// Deliberately not LoadOverlay. internal/kernel defines one too, and it is a
// different subject entirely: it reads a project's four .agentic-sdlc/
// documents. Two unrelated things may share a generic name; keying on it made
// this check report a duplicate that is not one, and the tempting response to
// that noise is to loosen the check rather than narrow the list.
func overlayEntryPoints() []string {
	return []string{"MergeRouting", "ResolveEffectiveRouting"}
}

// definitionSites returns every Go file under internal/ defining one of the
// named functions at top level.
func definitionSites(t *testing.T, name string) []string {
	t.Helper()
	root := filepath.Dir(filepath.Dir(mustWorkingDirectory(t)))
	pattern := regexp.MustCompile(`(?m)^func ` + regexp.QuoteMeta(name) + `\(`)

	var sites []string
	err := filepath.WalkDir(filepath.Join(root, "internal"),
		func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") ||
				strings.HasSuffix(path, "_test.go") {
				return nil
			}
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			if pattern.Match(content) {
				relative, _ := filepath.Rel(root, path)
				sites = append(sites, relative)
			}
			return nil
		})
	if err != nil {
		t.Fatalf("walking internal/: %v", err)
	}
	return sites
}

// livesInSelector reports whether a definition site is in this package.
//
// Split out so it can be falsified directly. Moving overlay.go to another
// package to test this branch does not compile -- the rest of this package
// calls into it -- so a mutation there proves the compiler works, not that
// this check does.
func livesInSelector(site string) bool {
	return strings.HasPrefix(site, filepath.Join("internal", "selector")+string(filepath.Separator))
}

func mustWorkingDirectory(t *testing.T) string {
	t.Helper()
	working, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return working
}

func TestOnlyOnePackageImplementsTheOverlayMerge(t *testing.T) {
	// Each entry point exactly once, in this package. A second definition
	// compiles fine -- they are in different packages -- so nothing but a
	// count notices.
	for _, name := range overlayEntryPoints() {
		sites := definitionSites(t, name)
		switch {
		case len(sites) == 0:
			t.Errorf("%s is defined nowhere; this check has lost its subject", name)
		case len(sites) > 1:
			t.Errorf("%s is defined in %d places: %v\n"+
				"Two overlay implementations means a reader cannot tell which is "+
				"authoritative, and a fix applied to the wrong one passes its own "+
				"tests while changing nothing.", name, len(sites), sites)
		case !livesInSelector(sites[0]):
			t.Errorf("%s moved to %s; `cadre select` calls this package's copy",
				name, sites[0])
		}
	}
}

func TestTheDefinitionScanWouldNoticeASecondCopy(t *testing.T) {
	// Guards the guard. The check above passes over a tree with one definition
	// each, which is also what it would do if the pattern matched nothing --
	// and "defined nowhere" is caught, but only because that case is written
	// out. This proves the pattern finds what it claims to.
	sites := definitionSites(t, "MergeRouting")
	if len(sites) != 1 {
		t.Fatalf("expected exactly one MergeRouting, found %v", sites)
	}
	content, err := os.ReadFile(filepath.Join(
		filepath.Dir(filepath.Dir(mustWorkingDirectory(t))), sites[0]))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "func MergeRouting(") {
		t.Errorf("%s was reported as defining MergeRouting and does not", sites[0])
	}

	// A name nothing defines finds nothing, so the walk is not simply
	// returning every file it sees.
	if found := definitionSites(t, "MergeRoutingThatNobodyWrote"); len(found) != 0 {
		t.Errorf("the scan reported definitions of a function nobody wrote: %v", found)
	}
}

func TestTheLocationCheckDistinguishesThisPackageFromAnother(t *testing.T) {
	// The branch that catches the merge migrating somewhere else. Asserted on
	// the predicate rather than by moving the file, because that move does not
	// compile and so proves nothing about this check.
	for _, inside := range []string{
		filepath.Join("internal", "selector", "overlay.go"),
		filepath.Join("internal", "selector", "nested", "overlay.go"),
	} {
		if !livesInSelector(inside) {
			t.Errorf("%s is in this package and was not recognised as such", inside)
		}
	}
	for _, outside := range []string{
		filepath.Join("internal", "orchestration", "overlay.go"),
		filepath.Join("internal", "kernel", "overlay.go"),
		// The prefix trap: a sibling package whose name starts with the same
		// letters. Without the separator, "internal/selectorv2" reads as inside.
		filepath.Join("internal", "selectorv2", "overlay.go"),
	} {
		if livesInSelector(outside) {
			t.Errorf("%s is in another package and was accepted as this one", outside)
		}
	}
}
