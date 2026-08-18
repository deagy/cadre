// Package version resolves Cadre's distribution version from its marker file.
//
// A leaf package, imported by both internal/cli (for `cadre --version`) and
// internal/generators (which stamps the version into a generated plugin).
// Those had two independent implementations of the same parse, and when the
// marker moved, one was updated and the other silently started failing --
// `cadre generate-plugin` reported "cannot read CLI version marker" while
// `cadre --version` worked fine. One implementation, one place to change.
package version

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	cadre "github.com/deagy/cadre/cli"
)

// versionAssignment matches the version literal in the VERSION file.
//
// The file holds a bare version string on one line. The `VERSION = "x.y.z"`
// assignment form is still accepted because that is what cadre_cli/_version.py
// contained, and a plugin or wheel built before this changed still carries the
// old shape -- a CLI that reported no version at all when reading an older
// installation's marker would be a worse outcome than accepting both.
var versionAssignment = regexp.MustCompile(
	`(?m)^(?:VERSION\s*=\s*)?(?:"([^"]*)"|'([^']*)'|([0-9][^\s]*))\s*$`)

// VersionMarkerNames are the marker filenames tried, in order.
//
// VERSION is a plain text file: the Go CLI always parsed the old Python
// marker as text rather than executing it, so the .py extension bought
// nothing and cost this distribution its last Python file.
var VersionMarkerNames = []string{"VERSION", filepath.Join("cadre_cli", "_version.py")}

// Resolve returns Cadre's distribution version by locating and parsing the
// VERSION marker.
//
// It looks beside the installation root, which is the one place every channel
// agrees on: <repo>/VERSION in a checkout, <plugin>/suite/VERSION in a
// packaged plugin, <prefix>/share/cadre/VERSION in a pip/pipx wheel.
//
// The wheel case is why this changed. The old resolution looked for
// <repoRoot>/cadre_cli/_version.py and then, as a wheel fallback, for
// _version.py one directory *above* the root -- a layout the pure-Python
// wheel happened to produce. The binary wheel does not vendor cadre_cli at
// all, so `cadre --version` from an installed distribution failed outright
// with "could not read Cadre version marker". A smoke test that ran
// `cadre help` and `cadre select` did not notice, because neither reads it.
func Resolve(repoRoot string) (string, error) {
	candidates := make([]string, 0, len(VersionMarkerNames)*2)
	for _, name := range VersionMarkerNames {
		candidates = append(candidates, filepath.Join(repoRoot, name))
	}
	// The historical wheel layout put the marker one level above the root.
	// Kept so an older installation still reports its version.
	candidates = append(candidates, filepath.Join(filepath.Dir(repoRoot), "_version.py"))

	var lastPath string
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		lastPath = candidate
		contents, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		if submatches := findVersionSubmatches(versionAssignment, contents); submatches != nil {
			return *submatches, nil
		}
	}

	// No readable marker. Fall back to the version compiled into this
	// binary, which is the right answer precisely when there is no
	// installation to ask: a released archive ships the executable alone, and
	// `cadre --version` on it used to fail outright.
	//
	// Markers still win when present. A packaged plugin or an installed wheel
	// must report its own version rather than that of whichever binary is
	// reading it, and those two can differ across an upgrade.
	if embedded := cadre.Version(); embedded != "" {
		return embedded, nil
	}

	if lastPath != "" {
		return "", fmt.Errorf("could not find VERSION in Cadre version marker: %s", lastPath)
	}
	return "", fmt.Errorf("could not read Cadre version marker under %s", repoRoot)
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
	// loc[2],loc[3] = double-quoted; loc[4],loc[5] = single-quoted;
	// loc[6],loc[7] = a bare version with no quotes, which is what the plain
	// VERSION file contains.
	if loc[6] != -1 {
		value := string(contents[loc[6]:loc[7]])
		return &value
	}
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

// ParseMarker reads a version out of a marker file's contents.
//
// Exported so the release gate compares versions the same way `cadre
// --version` reads them. Two independent parsers is how a marker's format and
// its reader drift apart, and the release gate only fails when a release
// silently does not happen -- which nobody notices until they go looking for
// the artifact.
//
// Reports false when nothing in the contents looks like a version, which is
// distinct from an absent file: a marker that exists but cannot be read is a
// problem to report, not a version that did not change.
func ParseMarker(contents []byte) (string, bool) {
	if value := findVersionSubmatches(versionAssignment, contents); value != nil {
		return *value, true
	}
	return "", false
}
