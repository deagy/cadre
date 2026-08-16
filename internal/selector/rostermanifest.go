package selector

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A roster package's manifest: how the platform learns a roster's layout
// instead of assuming one.
//
// `cadre select --roster <dir>` points the selector at a roster package. Which
// file in that package holds the catalog, and which holds the routing rules, is
// the package's business -- it declares them in roster.json. Resolving
// <dir>/catalog.yaml and <dir>/orchestration/routing.json directly works for
// exactly one roster: the one this repository ships, whose manifest happens to
// declare those paths.
//
// Ported from roster/orchestration/src/roster_manifest.py.

// RosterManifestFilename is the bootstrap name, and the one path that cannot
// itself be indirected: something has to know what the manifest is called in
// order to open it.
const RosterManifestFilename = "roster.json"

// SupportedRosterSchemaVersion is the only manifest schema this selector
// understands. A newer package is refused by version rather than being read
// with today's assumptions about what its fields mean.
const SupportedRosterSchemaVersion = 1

// RosterManifest is a validated manifest with every declared path resolved.
type RosterManifest struct {
	Root             string
	ManifestPath     string
	ID               string
	Version          string
	Catalog          string
	Routing          string
	RoleRoot         string
	SharedPolicyRoot string
}

// RosterManifestError is a manifest that is missing, malformed, or declares an
// unusable path.
type RosterManifestError struct{ message string }

func (e *RosterManifestError) Error() string { return e.message }

func rosterManifestErrorf(format string, args ...any) error {
	return &RosterManifestError{message: fmt.Sprintf(format, args...)}
}

// LoadRosterManifest reads and validates the manifest at root.
//
// A missing manifest is refused rather than defaulted. Falling back to this
// repository's own layout would mean a package that is not a roster package
// silently dispatches Cadre's roles -- the caller asked for one roster and got
// another, with a plan that looks entirely normal.
func LoadRosterManifest(root string) (*RosterManifest, error) {
	resolvedRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, rosterManifestErrorf("roster package at %s cannot be resolved: %s", root, err)
	}
	if evaluated, err := filepath.EvalSymlinks(resolvedRoot); err == nil {
		resolvedRoot = evaluated
	}

	manifestPath := filepath.Join(resolvedRoot, RosterManifestFilename)
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, rosterManifestErrorf("roster package at %s is missing %s",
				resolvedRoot, RosterManifestFilename)
		}
		return nil, rosterManifestErrorf("%s: %s", manifestPath, err)
	}

	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, rosterManifestErrorf("%s: invalid JSON: %s", manifestPath, err)
	}

	// Every required field, named together. Reporting them one at a time makes
	// a half-written manifest a sequence of edit-and-retry rounds.
	var missing []string
	for _, field := range []string{
		"schema_version", "id", "version",
		"catalog", "routing", "role_root", "shared_policy_root",
	} {
		if _, present := document[field]; !present {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		return nil, rosterManifestErrorf("%s: missing required field(s): %s",
			manifestPath, strings.Join(missing, ", "))
	}

	version, ok := jsonInteger(document["schema_version"])
	if !ok || version != SupportedRosterSchemaVersion {
		return nil, rosterManifestErrorf(
			"%s: unsupported schema_version %v; this selector supports %d",
			manifestPath, document["schema_version"], SupportedRosterSchemaVersion)
	}

	manifest := &RosterManifest{Root: resolvedRoot, ManifestPath: manifestPath}
	for _, field := range []struct {
		name      string
		target    *string
		directory bool
	}{
		{"catalog", &manifest.Catalog, false},
		{"routing", &manifest.Routing, false},
		{"role_root", &manifest.RoleRoot, true},
		{"shared_policy_root", &manifest.SharedPolicyRoot, true},
	} {
		resolved, err := rosterResource(resolvedRoot, document[field.name], field.name, field.directory)
		if err != nil {
			return nil, err
		}
		*field.target = resolved
	}
	for _, field := range []struct {
		name   string
		target *string
	}{{"id", &manifest.ID}, {"version", &manifest.Version}} {
		text, ok := document[field.name].(string)
		if !ok || text == "" {
			return nil, rosterManifestErrorf("%s: field %s must be a non-empty string",
				manifestPath, pythonRepr(field.name))
		}
		*field.target = text
	}
	return manifest, nil
}

// rosterResource resolves one declared path, rejecting anything that escapes
// root.
func rosterResource(root string, value any, field string, directory bool) (string, error) {
	text, ok := value.(string)
	if !ok || text == "" {
		return "", rosterManifestErrorf(
			"roster manifest field %s must be a non-empty relative path", pythonRepr(field))
	}
	// filepath.Join drops root when text is absolute, exactly as pathlib's `/`
	// does -- so an absolute value surfaces here as an escape rather than
	// needing its own guard. Left as the single check on purpose: a later
	// refactor that "simplifies" this into a prefix test on the raw string
	// would stop catching `..` segments.
	candidate := filepath.Join(root, text)
	if filepath.IsAbs(text) {
		candidate = filepath.Clean(text)
	}
	candidate, err := filepath.Abs(candidate)
	if err != nil {
		return "", rosterManifestErrorf("roster manifest field %s: %s", pythonRepr(field), err)
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", rosterManifestErrorf(
			"roster manifest field %s escapes its manifest directory: %s",
			pythonRepr(field), pythonRepr(text))
	}

	info, err := os.Stat(candidate)
	if err != nil {
		if directory {
			return "", rosterManifestErrorf(
				"roster manifest field %s names a directory that does not exist: %s",
				pythonRepr(field), pythonRepr(text))
		}
		return "", rosterManifestErrorf(
			"roster manifest field %s names a file that does not exist: %s",
			pythonRepr(field), pythonRepr(text))
	}
	if directory && !info.IsDir() {
		return "", rosterManifestErrorf(
			"roster manifest field %s names a directory that does not exist: %s",
			pythonRepr(field), pythonRepr(text))
	}
	if !directory && info.IsDir() {
		return "", rosterManifestErrorf(
			"roster manifest field %s names a file that does not exist: %s",
			pythonRepr(field), pythonRepr(text))
	}
	return candidate, nil
}
