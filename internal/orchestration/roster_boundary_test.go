package orchestration

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// PP-FR-6, second half: which platform files may name a roster's layout.
//
// The selector resolves a roster package from a manifest, so it can dispatch a
// roster this repository did not write. That only works while the platform
// stays ignorant of any particular roster.
//
// The first half of the rule -- no platform file hardcodes a role id -- is
// already held by platform_role_ids_test.go, which covers more packages than
// this one and parses the catalog properly. This file deliberately does not
// repeat it: a second copy of a guard is how the two drift into disagreeing,
// and the one with weaker coverage is the one somebody reads.
//
// What had no equivalent is the path half. Ported from
// roster/orchestration/test/test_roster_boundary.py's
// TestNoRosterPackagePathsInResolution.

// platformGoFiles are the packages that resolve, route, and dispatch. They run
// against whatever roster the manifest names.
func platformGoFiles(t *testing.T) []string {
	t.Helper()
	root := checkoutRoot(t)
	var files []string
	for _, base := range []string{
		filepath.Join("internal", "selector"),
		filepath.Join("internal", "orchestration"),
		filepath.Join("internal", "config"),
	} {
		err := filepath.WalkDir(filepath.Join(root, base),
			func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() || !strings.HasSuffix(path, ".go") ||
					strings.HasSuffix(path, "_test.go") {
					return nil
				}
				files = append(files, path)
				return nil
			})
		if err != nil {
			t.Fatalf("walking %s: %v", base, err)
		}
	}
	return files
}

func TestPlatformRosterPathsAreDeliberate(t *testing.T) {
	// The other half of PP-FR-6, kept as an allowlist rather than a ban.
	//
	// Naming roster/catalog.yaml is not wrong by itself -- `cadre doctor` asks
	// whether the cwd looks like a checkout, and team-recipe expansion reads
	// the *installation's* routing.json on purpose, because letting a project
	// supply its own recipes would let it define teams the roster never
	// sanctioned.
	//
	// What must not happen is a seventh site appearing without anyone deciding
	// it should. Each of these was read and justified; a new one fails here
	// until it is added with a reason.
	allowed := map[string]string{
		"internal/orchestration/dispatch_core_phase1.go": "locateCatalog: the installation's own catalog, found via FindInstallationRoot",
		"internal/orchestration/doctor.go":               "a diagnostic asking whether the cwd looks like a cadre checkout",
		"internal/orchestration/schema_validate.go":      "validates this repository's own roster; a self-check, not resolution",
		"internal/orchestration/team_recipe_expand.go":   "the installation's routing.json, deliberately not the project's",
		"internal/orchestration/manifest.go":             "the manifest loader, which is allowed to name the bootstrap file",
		"internal/selector/rostermanifest.go":            "the manifest loader",
	}
	resolution := regexp.MustCompile(
		`(filepath\.Join|os\.(Open|ReadFile|Stat))[^\n]*"(catalog\.yaml|routing\.json|roster\.json)"`)

	var unexpected []string
	for _, path := range platformGoFiles(t) {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		relative, _ := filepath.Rel(checkoutRoot(t), path)
		relative = filepath.ToSlash(relative)
		for number, text := range strings.Split(string(content), "\n") {
			if strings.HasPrefix(strings.TrimSpace(text), "//") {
				continue
			}
			if resolution.MatchString(text) {
				if _, ok := allowed[relative]; !ok {
					unexpected = append(unexpected,
						relative+":"+strconv.Itoa(number+1)+"  "+strings.TrimSpace(text))
				}
			}
		}
	}
	sort.Strings(unexpected)
	if len(unexpected) > 0 {
		t.Errorf("%d platform site(s) resolve a roster path without being on the "+
			"allowlist:\n  %s\n\nIf this is deliberate -- the installation's own "+
			"roster rather than a manifest-resolved one -- add it to the map above "+
			"with the reason.", len(unexpected), strings.Join(unexpected, "\n  "))
	}
}
