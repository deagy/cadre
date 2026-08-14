package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// versionAssignment matches a top-level `VERSION = "x.y.z"` (or single-quoted)
// literal assignment, mirroring bin/cadre.py's cli_version(), which parses
// the marker file's AST looking for exactly this shape rather than importing
// the module -- so that asking for a version cannot run package code. A
// regex is not an AST parser, but it is applied to a single-purpose marker
// file this CLI itself controls the format of (see cadre_cli/_version.py's
// own docstring), not arbitrary Python, so matching the one literal
// assignment shape that file is documented to contain is sufficient and
// keeps this dependency-free.
var versionAssignment = regexp.MustCompile(`(?m)^VERSION\s*=\s*(?:"([^"]*)"|'([^']*)')\s*$`)

// CLIVersion returns Cadre's pip/pipx distribution version by locating and
// parsing the VERSION marker, without executing any Python.
//
// This is an exact behavioral replica of bin/cadre.py's cli_version():
// cadre_cli/_version.py is pyproject.toml's sole version source, and it
// deliberately differs from provider.json's version (that one versions the
// Agentic SDLC provider manifest, not this CLI distribution).
//
// The source-checkout dispatcher lives at <repo>/bin/cadre.py (mirrored here
// as <repo>/cmd/cadre), and locates the marker at
// <repo>/cadre_cli/_version.py; a built wheel vendors the same layout one
// level up, at <install>/cadre_cli/_version.py relative to the vendored
// bin/ directory's parent's parent. repoRoot is the caller-resolved
// equivalent of bin/cadre.py's REPO_ROOT.
func CLIVersion(repoRoot string) (string, error) {
	checkoutMarker := filepath.Join(repoRoot, "cadre_cli", "_version.py")
	versionMarker := checkoutMarker
	if info, err := os.Stat(checkoutMarker); err != nil || info.IsDir() {
		// Mirror REPO_ROOT.parent / "_version.py" from cadre.py: the
		// vendored-wheel layout one directory above the checkout root.
		versionMarker = filepath.Join(filepath.Dir(repoRoot), "_version.py")
	}

	contents, err := os.ReadFile(versionMarker)
	if err != nil {
		return "", fmt.Errorf("could not read Cadre version marker: %w", err)
	}

	submatches := findVersionSubmatches(versionAssignment, contents)
	if submatches == nil {
		return "", fmt.Errorf("could not find VERSION in Cadre version marker: %s", versionMarker)
	}
	return *submatches, nil
}

// findVersionSubmatches returns a pointer to the matched VERSION literal's
// text, or nil if the pattern did not match at all. FindSubmatchIndex (as
// opposed to FindSubmatch) reports -1 for a capture group that did not
// participate in the match, which is what distinguishes "the single-quoted
// alternative matched an empty string" from "the double-quoted alternative
// is the one that matched" -- FindSubmatch alone cannot make that
// distinction, since both cases report a non-nil, zero-length byte slice.
func findVersionSubmatches(re *regexp.Regexp, contents []byte) *string {
	loc := re.FindSubmatchIndex(contents)
	if loc == nil {
		return nil
	}
	// loc[2],loc[3] = double-quoted group; loc[4],loc[5] = single-quoted group.
	if loc[2] != -1 {
		value := string(contents[loc[2]:loc[3]])
		return &value
	}
	if loc[4] != -1 {
		value := string(contents[loc[4]:loc[5]])
		return &value
	}
	return nil
}
