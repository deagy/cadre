package orchestration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const ManifestFilename = "roster.json"
const ManifestSchemaVersion = 1

// RosterManifest is a validated roster package manifest with all paths resolved.
type RosterManifest struct {
	Root               string `json:"-"`
	ManifestPath       string `json:"-"`
	ID                 string `json:"id"`
	Version            string `json:"version"`
	SchemaVersion      int    `json:"schema_version"`
	Catalog            string `json:"catalog"`
	Routing            string `json:"routing"`
	RoleRoot           string `json:"role_root"`
	SharedPolicyRoot   string `json:"shared_policy_root"`
	// Resolved absolute paths
	CatalogPath          string `json:"-"`
	RoutingPath          string `json:"-"`
	RoleRootPath         string `json:"-"`
	SharedPolicyRootPath string `json:"-"`
}

// LoadRosterManifest reads and validates <root>/roster.json
// Fails closed and by name: missing manifest, malformed JSON, unknown schema version,
// missing required fields, paths that escape or don't exist each raise named errors.
func LoadRosterManifest(root string) (*RosterManifest, error) {
	rootPath, err := filepath.Abs(filepath.FromSlash(root))
	if err != nil {
		return nil, fmt.Errorf("cannot resolve roster root: %w", err)
	}

	manifestPath := filepath.Join(rootPath, ManifestFilename)
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("roster package at %s is missing %s", rootPath, ManifestFilename)
		}
		return nil, fmt.Errorf("cannot read %s: %w", manifestPath, err)
	}

	var manifest RosterManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("%s must contain JSON: %w", manifestPath, err)
	}

	manifest.Root = rootPath
	manifest.ManifestPath = manifestPath

	// Validate schema version
	if manifest.SchemaVersion != ManifestSchemaVersion {
		return nil, fmt.Errorf("%s: unsupported schema_version %d; this selector supports %d",
			manifestPath, manifest.SchemaVersion, ManifestSchemaVersion)
	}

	// Validate required string fields
	if manifest.ID == "" {
		return nil, fmt.Errorf("%s: field 'id' must be a non-empty string", manifestPath)
	}
	if manifest.Version == "" {
		return nil, fmt.Errorf("%s: field 'version' must be a non-empty string", manifestPath)
	}

	// Resolve and validate path fields
	requiredPaths := map[string]*string{
		"catalog":             &manifest.CatalogPath,
		"routing":             &manifest.RoutingPath,
		"role_root":           &manifest.RoleRootPath,
		"shared_policy_root":  &manifest.SharedPolicyRootPath,
	}

	for fieldName, resolvedPtr := range requiredPaths {
		var rawPath string
		var isDir bool
		switch fieldName {
		case "catalog":
			rawPath = manifest.Catalog
			isDir = false
		case "routing":
			rawPath = manifest.Routing
			isDir = false
		case "role_root":
			rawPath = manifest.RoleRoot
			isDir = true
		case "shared_policy_root":
			rawPath = manifest.SharedPolicyRoot
			isDir = true
		}

		resolved, err := resolveManifestPath(rootPath, rawPath, fieldName, isDir)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", manifestPath, err)
		}
		*resolvedPtr = resolved
	}

	return &manifest, nil
}

// resolveManifestPath resolves one declared path, rejecting anything that escapes root.
// Root must already be an absolute path.
func resolveManifestPath(root, value, field string, isDir bool) (string, error) {
	if value == "" {
		return "", fmt.Errorf("field %q must be a non-empty relative path", field)
	}

	// Join and resolve to absolute path
	candidate := filepath.Join(root, value)
	candidate, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("field %q: cannot resolve path: %w", field, err)
	}

	// Check containment: candidate must be within root
	rel, err := filepath.Rel(root, candidate)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || len(rel) > 2 && rel[:3] == "../" {
		return "", fmt.Errorf("field %q escapes its manifest directory: %q", field, value)
	}

	// Check existence and type
	info, err := os.Stat(candidate)
	if err != nil {
		if os.IsNotExist(err) {
			if isDir {
				return "", fmt.Errorf("field %q names a directory that does not exist: %q", field, value)
			}
			return "", fmt.Errorf("field %q names a file that does not exist: %q", field, value)
		}
		return "", fmt.Errorf("field %q: cannot stat path: %w", field, err)
	}

	if isDir && !info.IsDir() {
		return "", fmt.Errorf("field %q names a directory but is a file: %q", field, value)
	}
	if !isDir && info.IsDir() {
		return "", fmt.Errorf("field %q names a file but is a directory: %q", field, value)
	}

	return candidate, nil
}

// DefaultRosterRoot returns this installation's own roster, derived from this package's location.
// For a source checkout: <repo>/roster
// For a packaged plugin: <plugin_root>/roster (if it exists)
func DefaultRosterRoot() string {
	// This package lives at internal/orchestration, so we go up 2 levels to repo root
	// then down to roster/
	wd, _ := os.Getwd()
	return filepath.Join(wd, "roster")
}
