// shared_overlay.go ports roster/shared/src/resolve.py's remaining unique
// content (find_file_at_project_root and deep_merge are consolidated
// elsewhere -- see platform.FindFileAtProjectRoot and DeepMergeJSON):
// resolving the effective content of a roster/shared/<filename> default,
// optionally extended or overridden by a project-local
// .agents/shared/<filename> overlay.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/deagy/cadre/cli/internal/platform"
	"gopkg.in/yaml.v3"
)

// ProjectOverlayRelativeDir is where a project-local roster/shared/
// override lives, relative to the project root.
var ProjectOverlayRelativeDir = filepath.Join(".agents", "shared")

// OverlayError reports a malformed project-local overlay, or one that
// violates a merge rule (currently only agent-autonomy.yaml's
// narrowing-only rule). Mirrors resolve.py's OverlayError.
type OverlayError struct{ msg string }

func (e *OverlayError) Error() string { return e.msg }

func overlayErrorf(format string, args ...any) error {
	return &OverlayError{msg: fmt.Sprintf(format, args...)}
}

// FindProjectOverlay walks upward from start (empty string means cwd) for
// a project-local .agents/shared/<filename>. Stops at the first directory
// containing that file, or at the first directory containing .git (the
// project boundary) if no match is found first.
func FindProjectOverlay(filename, start string) (string, bool) {
	return platform.FindFileAtProjectRoot(filepath.Join(ProjectOverlayRelativeDir, filename), start)
}

func loadStructured(path string) (map[string]any, error) {
	text, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(text)) == "" {
		// An emptied structured file is a deliberate "no content recorded"
		// state (e.g. a project intentionally clearing a shared default),
		// not malformed input -- treat it the same as an absent file.
		return map[string]any{}, nil
	}
	var loaded any
	if strings.EqualFold(filepath.Ext(path), ".json") {
		if err := json.Unmarshal(text, &loaded); err != nil {
			return nil, overlayErrorf("%s: invalid JSON: %v", path, err)
		}
	} else {
		if err := yaml.Unmarshal(text, &loaded); err != nil {
			return nil, overlayErrorf("%s: invalid YAML: %v", path, err)
		}
	}
	m, ok := loaded.(map[string]any)
	if !ok {
		return nil, overlayErrorf("%s: root must be a mapping", path)
	}
	return m, nil
}

// ---------------------------------------------------------------------
// agent-autonomy.yaml's narrowing-only overlay rule.
// ---------------------------------------------------------------------

const autonomyFilename = "agent-autonomy.yaml"

// autonomyRestrictivenessRank is every leaf value that appears in
// agent-autonomy.yaml, ranked from least restrictive (0) to most
// restrictive (10). See resolve.py's own extensive commentary on why each
// tier sits where it does -- carried over verbatim in spirit, condensed
// here to the ranking itself since the reasoning doesn't change per port.
var autonomyRestrictivenessRank = map[string]int{
	"allowed":                       0,
	"allowed_within_selected_scope": 1,
	"allowed_with_explicit_read_only_credentials": 2,
	"on_request":                                       3,
	"explicit_task_authorization":                      4,
	"explicitly_authorized":                            5,
	"explicitly_authorized_and_minimum_scope":          6,
	"human_approval_except_authorized_disposable_test": 7,
	"human_approval":                                   8,
	"knowledge_store_steward_only":                     9,
	"never":                                            10,
}

var autonomyFixedKeys = map[string]bool{"policy_version": true, "default_rule": true}

type autonomyLeaf struct {
	path  string
	value any
}

func autonomyLeafPaths(node map[string]any, prefix string) []autonomyLeaf {
	var paths []autonomyLeaf
	// Sorted iteration for deterministic error ordering across runs.
	keys := make([]string, 0, len(node))
	for k := range node {
		keys = append(keys, k)
	}
	sortStringsLocal(keys)
	for _, key := range keys {
		value := node[key]
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		if m, ok := value.(map[string]any); ok {
			paths = append(paths, autonomyLeafPaths(m, path)...)
		} else {
			paths = append(paths, autonomyLeaf{path: path, value: value})
		}
	}
	return paths
}

func sortStringsLocal(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

func autonomyRank(path string, value any) (int, error) {
	if s, ok := value.(string); ok {
		if rank, ok := autonomyRestrictivenessRank[s]; ok {
			return rank, nil
		}
	}
	return 0, overlayErrorf("%s: %v is not a recognized agent-autonomy.yaml permission value", path, value)
}

// checkAutonomyOverlay enforces that an agent-autonomy.yaml overlay only
// narrows autonomy. Returns an *OverlayError if the overlay touches the
// fixed policy_version/default_rule keys, references a category or key the
// default doesn't define, sets a value outside the recognized ranked
// vocabulary, or moves a leaf value to a strictly lower (less restrictive)
// rank than the base default.
func checkAutonomyOverlay(base, overlay map[string]any) error {
	for fixedKey := range autonomyFixedKeys {
		if _, present := overlay[fixedKey]; present {
			return overlayErrorf("agent-autonomy.yaml overlay may not set %q; it is the fixed autonomy contract, not a per-project dial", fixedKey)
		}
	}
	baseValues := map[string]any{}
	for _, leaf := range autonomyLeafPaths(base, "") {
		baseValues[leaf.path] = leaf.value
	}
	for _, leaf := range autonomyLeafPaths(overlay, "") {
		defaultValue, ok := baseValues[leaf.path]
		if !ok {
			return overlayErrorf("agent-autonomy.yaml overlay references undefined key %q", leaf.path)
		}
		if defaultValue == leaf.value {
			// Still validate that an unchanged value is itself recognized;
			// a corrupted base default should not be able to smuggle a
			// bogus value through as a no-op.
			if _, err := autonomyRank(leaf.path, defaultValue); err != nil {
				return err
			}
			continue
		}
		defaultRank, err := autonomyRank(leaf.path, defaultValue)
		if err != nil {
			return err
		}
		overlayRank, err := autonomyRank(leaf.path, leaf.value)
		if err != nil {
			return err
		}
		if overlayRank < defaultRank {
			return overlayErrorf("%s: overlay may not loosen %v (rank %d) to %v (rank %d)",
				leaf.path, defaultValue, defaultRank, leaf.value, overlayRank)
		}
	}
	return nil
}

// ---------------------------------------------------------------------
// resolve_shared_config.
// ---------------------------------------------------------------------

// ResolveSharedConfigResult is ResolveSharedConfig's return shape:
// exactly one of Structured or Text is set, matching resolve_shared_config's
// dict-for-structured / str-for-Markdown dynamic return type.
type ResolveSharedConfigResult struct {
	Structured map[string]any // set for .yaml/.yml/.json defaults
	Text       string         // set for .md defaults
	IsText     bool
}

// ResolveSharedConfig returns the effective content for
// sharedDefaultsDir/filename (a roster/shared/<filename> default),
// optionally extended or overridden by a project-local
// .agents/shared/<filename> overlay found by walking up from start.
//
// Structured files (.yaml/.yml/.json) are deep-merged with the project
// overlay winning per key; agent-autonomy.yaml additionally rejects any
// overlay that loosens a restriction. Markdown files are returned as the
// base text with the overlay appended as a project addendum -- an overlay
// never replaces prose, it only adds to it.
func ResolveSharedConfig(sharedDefaultsDir, filename, start string) (ResolveSharedConfigResult, error) {
	defaultPath := filepath.Join(sharedDefaultsDir, filename)
	if info, err := os.Stat(defaultPath); err != nil || info.IsDir() {
		return ResolveSharedConfigResult{}, fmt.Errorf("no such shared default: %s", defaultPath)
	}
	overlayPath, overlayFound := FindProjectOverlay(filename, start)

	if strings.EqualFold(filepath.Ext(defaultPath), ".md") {
		baseText, err := os.ReadFile(defaultPath)
		if err != nil {
			return ResolveSharedConfigResult{}, err
		}
		if !overlayFound {
			return ResolveSharedConfigResult{Text: string(baseText), IsText: true}, nil
		}
		addendum, err := os.ReadFile(overlayPath)
		if err != nil {
			return ResolveSharedConfigResult{}, err
		}
		combined := fmt.Sprintf("%s\n## Project addendum (%s)\n\n%s", baseText, overlayPath, addendum)
		return ResolveSharedConfigResult{Text: combined, IsText: true}, nil
	}

	base, err := loadStructured(defaultPath)
	if err != nil {
		return ResolveSharedConfigResult{}, err
	}
	if !overlayFound {
		return ResolveSharedConfigResult{Structured: base}, nil
	}
	overlay, err := loadStructured(overlayPath)
	if err != nil {
		return ResolveSharedConfigResult{}, err
	}
	if filename == autonomyFilename {
		if err := checkAutonomyOverlay(base, overlay); err != nil {
			return ResolveSharedConfigResult{}, err
		}
	}
	return ResolveSharedConfigResult{Structured: DeepMergeJSON(base, overlay)}, nil
}

// ---------------------------------------------------------------------
// Exports for internal/initproject, which -- like init_project.py reaching
// into resolve.py's underscore-prefixed internals directly, since both
// live in roster/shared/src/ -- needs this package's autonomy-overlay
// internals directly rather than only the top-level ResolveSharedConfig
// entry point.
// ---------------------------------------------------------------------

// AutonomyFilename is agent-autonomy.yaml's exact filename, the one
// roster/shared/ file with narrowing-only overlay semantics.
const AutonomyFilename = autonomyFilename

// AutonomyRestrictivenessRank is the exported form of this file's
// autonomy-value ranking table.
var AutonomyRestrictivenessRank = autonomyRestrictivenessRank

// AutonomyFixedKeys is the exported form of this file's fixed
// (never-overlay-settable) agent-autonomy.yaml top-level keys.
var AutonomyFixedKeys = autonomyFixedKeys

// AutonomyLeaf is one dotted-path/value pair from an agent-autonomy.yaml
// structure (or fragment).
type AutonomyLeaf struct {
	Path  string
	Value any
}

// AutonomyLeafPaths returns every dotted-path/value leaf in node, sorted by
// path for deterministic ordering.
func AutonomyLeafPaths(node map[string]any) []AutonomyLeaf {
	raw := autonomyLeafPaths(node, "")
	out := make([]AutonomyLeaf, len(raw))
	for i, l := range raw {
		out[i] = AutonomyLeaf{Path: l.path, Value: l.value}
	}
	return out
}

// AutonomyRank returns value's restrictiveness rank, or an *OverlayError if
// value is not a recognized agent-autonomy.yaml permission value.
func AutonomyRank(path string, value any) (int, error) {
	return autonomyRank(path, value)
}

// CheckAutonomyOverlay is the exported form of this file's narrowing-only
// enforcement, for callers (internal/initproject) that build a merged
// autonomy overlay themselves rather than going through ResolveSharedConfig.
func CheckAutonomyOverlay(base, overlay map[string]any) error {
	return checkAutonomyOverlay(base, overlay)
}

// LoadStructured reads and parses a structured (.yaml/.yml/.json)
// roster/shared/ file -- either a shipped default or a project overlay.
func LoadStructured(path string) (map[string]any, error) {
	return loadStructured(path)
}
