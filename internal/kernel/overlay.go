package kernel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The project overlay: the four documents a project keeps under
// .agentic-sdlc/ that say what it is, who holds authority over it, what it is
// exposed to, and how work routes through the gates.
//
// This is the foundation `validate` is built on. It is landing before
// `validate` itself, and `validate` is deliberately not wired into the CLI
// yet: a partial implementation that skipped run-record checks would print
// {"valid": true} for a repository whose run records are broken, which is a
// worse answer than no answer. The subcommand appears when it can do the
// whole job.

// Overlay is the directory name a project's kernel state lives under.
const Overlay = ".agentic-sdlc"

// ProjectOverlay is the four documents load_overlay returns.
type ProjectOverlay struct {
	Root        string
	Project     map[string]any
	Authorities map[string]any
	Impact      map[string]any
	Routing     map[string]any
}

// ConfinedPath resolves a path under root and refuses one that escapes it.
//
// A port of confined_path. The kernel reads and writes project files whose
// names come from task ids and directory listings, and a symlink or junction
// inside the overlay is enough to redirect that anywhere the process can
// reach. Resolution happens first and containment is checked on the resolved
// result, because checking the unresolved path proves nothing about where it
// lands.
//
// Non-existent paths resolve too (strict=False in Python): the kernel builds
// paths for files it is about to create, and refusing to reason about them
// would make the check unusable exactly where it is needed.
func ConfinedPath(root string, parts ...string) (string, error) {
	resolvedRoot, err := resolveExisting(root)
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(append([]string{resolvedRoot}, parts...)...)
	resolvedCandidate, err := resolveExisting(candidate)
	if err != nil {
		return "", err
	}
	if resolvedCandidate == resolvedRoot {
		return candidate, nil
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedCandidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("project path escapes root: %s", candidate)
	}
	return candidate, nil
}

// resolveExisting resolves symlinks as far as the path exists, leaving the
// non-existent tail alone -- Python's Path.resolve(strict=False).
//
// Resolving only the existing prefix is the point: the leaf may not be there
// yet, but every directory it will be created under is, and those are where a
// swapped symlink would redirect the write.
func resolveExisting(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	remaining := ""
	current := absolute
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			if remaining == "" {
				return resolved, nil
			}
			return filepath.Join(resolved, remaining), nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return absolute, nil
		}
		remaining = filepath.Join(filepath.Base(current), remaining)
		current = parent
	}
}

// LoadOverlay reads a project's four overlay documents.
func LoadOverlay(root string) (*ProjectOverlay, error) {
	overlay, err := ConfinedPath(root, Overlay)
	if err != nil {
		return nil, err
	}
	loaded := &ProjectOverlay{Root: overlay}
	for _, document := range []struct {
		name   string
		target *map[string]any
	}{
		{"project.json", &loaded.Project},
		{"authorities.json", &loaded.Authorities},
		{"impact-profile.json", &loaded.Impact},
		{"routing.json", &loaded.Routing},
	} {
		object, err := loadJSONObject(filepath.Join(overlay, document.name))
		if err != nil {
			return nil, err
		}
		*document.target = object
	}
	return loaded, nil
}

// ApprovalPolicy is how a project's human gates are approved.
type ApprovalPolicy struct {
	HumanGateDefault    string
	AllowManualFallback bool
}

// ApprovalSourcePolicy reads and validates a project's approval_sources.
//
// The default is manual with a fallback allowed, which is the permissive
// reading -- but an *invalid* value is an error rather than a fall back to
// the default. A project that meant to require GitHub review and misspelled
// it would otherwise silently accept manual approvals, which is the failure
// this refuses to make quiet.
func ApprovalSourcePolicy(project map[string]any) (ApprovalPolicy, error) {
	raw, present := project["approval_sources"]
	if !present || raw == nil {
		return ApprovalPolicy{HumanGateDefault: "manual", AllowManualFallback: true}, nil
	}
	policy, ok := raw.(map[string]any)
	if !ok {
		return ApprovalPolicy{}, fmt.Errorf("project approval_sources must be a JSON object")
	}

	source := "manual"
	if value, present := policy["human_gate_default"]; present {
		text, ok := value.(string)
		if !ok {
			return ApprovalPolicy{}, fmt.Errorf(
				"project approval_sources.human_gate_default must be 'manual', 'github-review', or 'gitlab-mr'")
		}
		source = text
	}
	switch source {
	case "manual", "github-review", "gitlab-mr":
	default:
		return ApprovalPolicy{}, fmt.Errorf(
			"project approval_sources.human_gate_default must be 'manual', 'github-review', or 'gitlab-mr'")
	}

	allowFallback := true
	if value, present := policy["allow_manual_fallback"]; present {
		flag, ok := value.(bool)
		if !ok {
			return ApprovalPolicy{}, fmt.Errorf(
				"project approval_sources.allow_manual_fallback must be a boolean")
		}
		allowFallback = flag
	}
	return ApprovalPolicy{HumanGateDefault: source, AllowManualFallback: allowFallback}, nil
}

// LoadAgentCatalog merges every loaded provider's catalog.
//
// Later providers overwrite earlier entries for the same agent id, which is
// how a downstream provider refines an upstream one.
//
// A relative `definition` is resolved against its own provider's root and
// refused if it escapes -- a catalog entry naming ../../ is a provider
// pointing the kernel at a file outside anything it declared.
func (r *Registry) LoadAgentCatalog() (map[string]any, error) {
	merged := map[string]any{}
	for _, catalogPath := range r.CatalogRoots {
		if _, err := os.Stat(catalogPath); err != nil {
			continue
		}
		catalog, err := loadJSONObject(catalogPath)
		if err != nil {
			return nil, err
		}
		agents, ok := catalog["agents"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: agents must be an object", catalogPath)
		}
		providerRoot, err := resolveExisting(filepath.Dir(catalogPath))
		if err != nil {
			return nil, err
		}
		for agentID, raw := range agents {
			metadata, ok := raw.(map[string]any)
			if !ok {
				merged[agentID] = raw
				continue
			}
			definition, ok := metadata["definition"].(string)
			if ok && !filepath.IsAbs(definition) {
				resolved, err := resolveExisting(filepath.Join(providerRoot, definition))
				if err != nil {
					return nil, err
				}
				relative, err := filepath.Rel(providerRoot, resolved)
				if err != nil || relative == ".." ||
					strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
					return nil, fmt.Errorf("agent definition escapes provider root: %s", definition)
				}
				metadata["definition"] = resolved
			}
			merged[agentID] = metadata
		}
	}
	return merged, nil
}

// IsValidDatetime reports whether value is an ISO-8601 timestamp carrying an
// explicit timezone offset.
//
// The offset is the requirement, not the format. A run record's timestamps
// are evidence of when a gate was decided; one without an offset means
// whatever the reader's local zone happens to be, which makes an audit trail
// that says different things to different people.
func IsValidDatetime(value any) bool {
	text, ok := value.(string)
	if !ok {
		return false
	}
	// Python's datetime.fromisoformat is more permissive than RFC3339 in one
	// way that matters: it accepts a space where the standard requires "T",
	// so "2026-08-15 12:00:00+00:00" parses there and would not here.
	//
	// Found by running both against the same inputs rather than by reading
	// either. A Go kernel that rejected it would report an error the Python
	// kernel does not, on a run record that has been valid all along.
	if len(text) > 10 && text[10] == ' ' {
		text = text[:10] + "T" + text[11:]
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if _, err := time.Parse(layout, text); err == nil {
			// A successful parse with these layouts already implies an
			// explicit offset, which is the requirement: a timestamp without
			// one means whatever the reader's local zone is, and an audit
			// trail that says different things to different people is not one.
			return true
		}
	}
	return false
}
