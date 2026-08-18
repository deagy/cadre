// Package clinedeps resolves the dependency closure a Git-source Cline
// install actually ships.
//
// Cline discovers the three TypeScript entrypoints from the repository root
// when a user runs `cline plugin install https://github.com/deagy/cadre`.
// Unlike this repository's development workspace, that installation resolves
// bare runtime imports from a root node_modules.
//
// The root closure and cline-plugins/ declare the same runtime packages twice,
// and Dependabot updates them on independent pull requests, so either side can
// be bumped alone. A divergence would be invisible: CI would keep testing
// cline-plugins/'s versions while a Git-source install shipped the root's.
// Nothing here is hardcoded for that reason -- the expected closure is derived
// from the workspace manifests, so drift fails rather than passing against a
// stale literal.
//
// Ported from plugin/tools/test_cline_git_plugin_packaging.py.
package clinedeps

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Entrypoints are the three files Cline loads from the repository root.
var Entrypoints = []string{
	"./cline-plugins/cline-agents/index.ts",
	"./cline-plugins/cline-lifecycle/index.ts",
	"./cline-plugins/cline/index.ts",
}

// HostSuppliedScope is provided by Cline's host sandbox at runtime, so it is
// deliberately absent from the root closure even though the workspace packages
// declare it.
const HostSuppliedScope = "@cline/"

// exactVersionPin is an allow-list, not a denylist of range shapes.
//
// Enumerating every disallowed shape (^, ~, >=, a space-separated range, ||,
// an x/* wildcard segment, a - prerelease-range shorthand, ...) risks missing
// one and letting a range through undetected, which is the dangerous failure
// mode here. So anything not affirmatively of this shape is rejected.
//
// Build metadata (a + suffix) is deliberately not accepted: no manifest here
// declares that shape, and treating it as rejected keeps this a strict
// allow-list rather than one that grows implicitly.
//
// \z rather than $, because $ would also match before a trailing newline and
// let "4.4.3\n" through.
var exactVersionPin = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?\z`)

// LockPackage is one entry in a package-lock.json "packages" map.
//
// Integrity is a pointer because its absence is meaningful: a git- or
// file:-sourced entry legitimately has none, and missing has to compare as a
// distinct value rather than as the empty string, so a registry copy and a
// non-registry copy read as disagreeing instead of as equal.
type LockPackage struct {
	Version   string  `json:"version"`
	Integrity *string `json:"integrity"`
}

// identity is what two lock entries are compared on. A comparable struct, so
// "no integrity" is a value rather than an absence that has to be special-cased
// at every comparison -- which is how the original grew a fail-closed top-level
// path and a crashing fallback path in the same function.
type identity struct {
	version      string
	integrity    string
	hasIntegrity bool
}

func identityOf(entry LockPackage) identity {
	found := identity{version: entry.Version}
	if entry.Integrity != nil {
		found.integrity = *entry.Integrity
		found.hasIntegrity = true
	}
	return found
}

func (i identity) String() string {
	if !i.hasIntegrity {
		return i.version + " (no integrity field)"
	}
	return i.version
}

// Lockfile is the part of a package-lock.json this package reads.
type Lockfile struct {
	LockfileVersion int                    `json:"lockfileVersion"`
	Packages        map[string]LockPackage `json:"packages"`
}

// ReadJSON decodes a JSON file under root into target.
func ReadJSON(root string, target any, parts ...string) error {
	full := filepath.Join(append([]string{root}, parts...)...)
	content, err := os.ReadFile(full)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(content, target); err != nil {
		return fmt.Errorf("%s does not parse: %w", filepath.Join(parts...), err)
	}
	return nil
}

// WorkspaceRuntimeDependencies maps each plugin-owned runtime package to
// {workspace: declared version}.
//
// Every declaration is returned rather than a collapsed name->version map, so
// a caller can report *which* workspace disagrees when two of them pin the
// same package differently.
func WorkspaceRuntimeDependencies(root string, entrypoints []string) (map[string]map[string]string, error) {
	declarations := map[string]map[string]string{}
	for _, entrypoint := range entrypoints {
		// Each entrypoint is assumed to sit directly under
		// cline-plugins/<workspace>/. If one moved a level deeper --
		// cline-plugins/cline/dist/index.ts -- this resolves to "dist" and
		// every lookup below would silently target the wrong workspace.
		workspace := path.Base(path.Dir(entrypoint))
		manifestPath := filepath.Join(root, "cline-plugins", workspace, "package.json")
		if _, err := os.Stat(manifestPath); err != nil {
			// Naming the assumption, rather than letting a file-not-found
			// error point at a path nobody expected to exist. The derivation
			// is what broke; the missing file is only the symptom.
			return nil, fmt.Errorf(
				"entrypoint %q implies workspace %q, but cline-plugins/%s/package.json "+
					"does not exist. The workspace name is derived as the parent "+
					"directory of the entrypoint, which assumes each entrypoint sits "+
					"directly under cline-plugins/<workspace>/. If this entrypoint moved "+
					"a level deeper, that assumption is what needs updating, rather than "+
					"this manifest path", entrypoint, workspace, workspace)
		}
		var manifest struct {
			Dependencies map[string]string `json:"dependencies"`
		}
		if err := ReadJSON(root, &manifest, "cline-plugins", workspace, "package.json"); err != nil {
			return nil, err
		}
		for name, version := range manifest.Dependencies {
			if strings.HasPrefix(name, HostSuppliedScope) {
				continue
			}
			if declarations[name] == nil {
				declarations[name] = map[string]string{}
			}
			declarations[name][workspace] = version
		}
	}
	return declarations, nil
}

// RefuseInexactPins rejects any declared runtime dependency that is not an
// exact version pin.
//
// ResolveWorkspacePackageKey trusts a top-level node_modules/<dependency>
// entry only when its resolved version exactly string-equals one of our
// workspaces' declared pins. That equality has no semver evaluator behind it;
// it works only because every runtime dependency across all four package.json
// manifests is pinned exactly.
//
// If a bump ever introduced a real range, the exact-match check would simply
// never match, the lookup would fall through to the workspace-scoped keys,
// usually find nothing, and raise a confusing "not found" -- technically
// fail-closed, but exactly the kind of red build that tempts someone into
// "fixing" it by loosening the exact-match gate. Loosening that gate is what
// would silently reopen the version-exclusivity hole the gate exists to
// prevent, because it is what forces the resolver to fall through and verify
// exclusivity rather than trusting a top-level key that merely satisfies a
// range.
//
// So the precondition is asserted up front and by name. Deliberately not a
// semver range evaluator: a genuinely-needed range requires a proper, reviewed
// one added as a deliberate change, not a quick loosening here.
func RefuseInexactPins(declarations map[string]map[string]string) error {
	for _, name := range sortedKeys(declarations) {
		byWorkspace := declarations[name]
		for _, workspace := range sortedKeys(byWorkspace) {
			version := byWorkspace[workspace]
			if exactVersionPin.MatchString(version) {
				continue
			}
			return fmt.Errorf(
				"%s declares %s as %q, which is not an exact version pin (expected a "+
					"bare MAJOR.MINOR.PATCH, optionally with a prerelease suffix -- no ^, "+
					"~, >=, space-separated range, ||, or x/* wildcard segment). This "+
					"check does not evaluate semver ranges: it only does an exact-match "+
					"comparison against declared pins when resolving the lockfile key, "+
					"deliberately. To fix this: either pin %s to an exact version in "+
					"cline-plugins/%s/package.json, or, if a range is genuinely required, "+
					"extend this check with a proper semver range evaluator as a "+
					"deliberate, reviewed change rather than loosening that comparison",
				workspace, name, version, name, workspace)
		}
	}
	return nil
}

// ResolveWorkspacePackageKey finds the cline-plugins/package-lock.json
// "packages" key holding our dependency's own resolved install.
//
// npm hoists a package to the top-level node_modules/<dependency> slot only
// when nothing else in the tree forces a conflicting version there. When
// something does, one of our workspaces' correctly-pinned dependency can be
// pushed down into <workspace>/node_modules/<dependency> instead
// (cline-plugins is the lockfile root, so workspaces are keyed by name alone).
// A missing top-level key is therefore not necessarily a missing dependency.
//
// That is a different shape from node_modules/<other>/node_modules/<dependency>,
// which is an unrelated transitive dependant's own pin. Matching on the leaf
// package name alone could silently pick up somebody else's version and mask
// real drift, so the lookup is workspace-scoped rather than a name glob.
//
// A resolution being valid is not the same as it being the only one. Node
// resolves from the closest node_modules upward, so a workspace-scoped entry
// is what code in that workspace actually loads, even when a correct copy sits
// at the top level. Every path below establishes exclusivity, not existence.
func ResolveWorkspacePackageKey(
	packages map[string]LockPackage,
	dependency string,
	byWorkspace map[string]string,
) (string, error) {
	topLevelKey := "node_modules/" + dependency

	declaredPins := map[string]bool{}
	for _, version := range byWorkspace {
		declaredPins[strings.TrimLeft(version, "^~=")] = true
	}

	var scopedKeys []string
	for _, workspace := range sortedKeys(byWorkspace) {
		key := workspace + "/node_modules/" + dependency
		if _, present := packages[key]; present {
			scopedKeys = append(scopedKeys, key)
		}
	}

	// A top-level key existing is not proof it is *our* copy: the key encodes
	// the package name, not whose requirement won the slot. So it is trusted
	// only when its resolved version matches one of our own declared pins.
	if topLevel, present := packages[topLevelKey]; present && declaredPins[topLevel.Version] {
		want := identityOf(topLevel)
		var disagreeing []string
		for _, key := range scopedKeys {
			if identityOf(packages[key]) != want {
				disagreeing = append(disagreeing,
					fmt.Sprintf("%s at %s", key, identityOf(packages[key])))
			}
		}
		if len(disagreeing) > 0 {
			return "", fmt.Errorf(
				"%s resolves to %s at the top-level key %q, but a declaring workspace "+
					"also has its own copy at a different version: %s in "+
					"cline-plugins/package-lock.json. Node resolves from the closest "+
					"node_modules upward, so that workspace loads its own copy, not the "+
					"top-level one -- a valid top-level resolution is not proof it is the "+
					"only resolution",
				dependency, topLevel.Version, topLevelKey, strings.Join(disagreeing, ", "))
		}
		return topLevelKey, nil
	}

	if len(scopedKeys) == 0 {
		var tried []string
		for _, workspace := range sortedKeys(byWorkspace) {
			tried = append(tried, workspace+"/node_modules/"+dependency)
		}
		return "", fmt.Errorf(
			"%s was absent from the top-level key %q in cline-plugins/package-lock.json, "+
				"and from every workspace-scoped fallback key (%s). A missing top-level "+
				"key alone would just mean npm hoisting moved the dependency elsewhere; "+
				"this means it is genuinely absent from both shapes checked",
			dependency, topLevelKey, strings.Join(tried, ", "))
	}

	// Missing integrity is a distinct value here too, not a crash. The Python
	// this is ported from indexed integrity directly on this path while using
	// a safe lookup on the one above -- so a git- or file:-sourced sibling
	// raised a bare KeyError, which is the same defect the safe lookup above
	// was written to fix, still live on the other branch of the same function
	// and covered by no test.
	distinct := map[identity]bool{}
	for _, key := range scopedKeys {
		distinct[identityOf(packages[key])] = true
	}
	if len(distinct) != 1 {
		var described []string
		for _, key := range scopedKeys {
			described = append(described, fmt.Sprintf("%s at %s", key, identityOf(packages[key])))
		}
		return "", fmt.Errorf(
			"%s resolves to disagreeing versions across the workspace-scoped fallback "+
				"keys in cline-plugins/package-lock.json: %s. Refusing to silently pick "+
				"one -- this indicates real drift between workspaces' own pins, rather "+
				"than hoisting", dependency, strings.Join(described, ", "))
	}
	return scopedKeys[0], nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
