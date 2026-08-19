// Package provider loads provider manifests and merges profiles.
//
// Ported from engine/agentic_sdlc_langgraph/provider.py, which was itself a
// pure-function port of the legacy CLI's load_provider and merge_profile.
// Both are pure here for the same reason they are pure there: the legacy
// versions mutated module-level lists, which is harmless in a one-shot CLI and
// wrong for a graph factory that must be reentrant.
package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/deagy/cadre/cli/internal/canonicaljson"
)

// KernelVersion is the kernel a provider's kernel_compatibility range is
// checked against.
//
// The Python mirrored this by hand from the legacy script's VERSION constant,
// and its own docstring warned that the two had already drifted once. They had
// drifted again by the time this was ported: the Python said 0.13.0 while the
// kernel was 0.14.1, and provider/provider.json requires 0.13.2 or newer -- so
// the engine refused to load this repository's own secure-cloud provider,
// reporting it as "incompatible with kernel 0.13.0".
//
// Still a constant, because the engine may not link the kernel: roster-side
// packages ask, they do not import. What is new is that a test reads the
// kernel's own source as text and fails when the two disagree, so the next
// drift is caught where it happens rather than at the point someone cannot
// load a provider.
const KernelVersion = "0.14.2"

var (
	validAgentKinds        = map[string]bool{"author": true, "reviewer": true, "specialist": true}
	validAgentCapabilities = map[string]bool{"author": true, "reviewer": true, "dispatch": true}
	allowedManifestKeys    = map[string]bool{
		"schema_version": true, "id": true, "version": true, "kernel_compatibility": true,
		"agent_catalog": true, "profile_roots": true, "extension_roots": true,
		"dependencies": true, "dispatch_bindings": true,
	}
	identifierPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	semverPattern     = regexp.MustCompile(`^([0-9]+)\.([0-9]+)\.([0-9]+)$`)
)

// LoadedProvider is the result of loading and validating a manifest.
type LoadedProvider struct {
	ID             string
	Version        string
	ManifestSHA256 string
	CatalogSHA256  string
	Dependencies   []map[string]any
	AgentCatalog   map[string]map[string]any
	ProfileRoots   []string
	ExtensionRoots []string
}

// Fingerprint is a stable, key-sorted, whitespace-free JSON sha256 digest.
//
// canonicaljson rather than encoding/json: Go escapes HTML characters by
// default and Python escapes non-ASCII, so the two produce different bytes for
// the same value and therefore different digests.
func Fingerprint(value any) (string, error) {
	canonical, err := canonicaljson.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// SemverTuple parses a strict MAJOR.MINOR.PATCH version.
func SemverTuple(value string) ([3]int, error) {
	match := semverPattern.FindStringSubmatch(value)
	if match == nil {
		return [3]int{}, fmt.Errorf("invalid semantic version: %s", value)
	}
	var parts [3]int
	for i := 0; i < 3; i++ {
		parts[i], _ = strconv.Atoi(match[i+1])
	}
	return parts, nil
}

func compareSemver(left, right [3]int) int {
	for i := 0; i < 3; i++ {
		if left[i] != right[i] {
			if left[i] < right[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// VersionSatisfies reports minimum <= version < maximumExclusive.
func VersionSatisfies(version, minimum, maximumExclusive string) (bool, error) {
	parsedVersion, err := SemverTuple(version)
	if err != nil {
		return false, err
	}
	parsedMinimum, err := SemverTuple(minimum)
	if err != nil {
		return false, err
	}
	parsedMaximum, err := SemverTuple(maximumExclusive)
	if err != nil {
		return false, err
	}
	return compareSemver(parsedMinimum, parsedVersion) <= 0 && compareSemver(parsedVersion, parsedMaximum) < 0, nil
}

// ProviderResource resolves a manifest-relative path and refuses anything that
// escapes the manifest's own directory or is the wrong kind.
//
// The confinement check is the security-relevant half: a manifest is data, and
// a `../../etc` in it must not reach outside the provider.
func ProviderResource(root string, value any, fieldName string, directory bool) (string, error) {
	text, isText := value.(string)
	if !isText || text == "" {
		return "", fmt.Errorf("provider %s must be a non-empty relative path", fieldName)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		resolvedRoot = filepath.Clean(root)
	}
	candidate := filepath.Clean(filepath.Join(resolvedRoot, text))
	if resolved, err := filepath.EvalSymlinks(candidate); err == nil {
		candidate = resolved
	}
	relative, err := filepath.Rel(resolvedRoot, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("provider %s escapes its manifest directory", fieldName)
	}

	info, err := os.Stat(candidate)
	if directory {
		if err != nil || !info.IsDir() {
			return "", fmt.Errorf("provider %s directory does not exist: %s", fieldName, text)
		}
		return candidate, nil
	}
	if err != nil || info.IsDir() {
		return "", fmt.Errorf("provider %s file does not exist: %s", fieldName, text)
	}
	return candidate, nil
}

func loadJSONObject(path string) (map[string]any, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var value any
	if err := json.Unmarshal(contents, &value); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	object, isObject := value.(map[string]any)
	if !isObject {
		return nil, fmt.Errorf("%s must contain a JSON object", path)
	}
	return object, nil
}

// LoadProvider validates a provider manifest and everything it points at.
//
// Duplicate-id checks are evaluated only against alreadyLoaded, which the
// caller controls: loading the same manifest twice with none already loaded
// succeeds both times, exactly as the Python does.
func LoadProvider(manifestPath string, alreadyLoaded []LoadedProvider) (LoadedProvider, error) {
	var empty LoadedProvider

	path, err := filepath.Abs(manifestPath)
	if err != nil {
		return empty, err
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	manifest, err := loadJSONObject(path)
	if err != nil {
		return empty, err
	}

	if asFloat(manifest["schema_version"]) != 1 {
		return empty, fmt.Errorf("unsupported provider schema in %s", path)
	}

	var unknown []string
	for key := range manifest {
		if !allowedManifestKeys[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return empty, fmt.Errorf("provider manifest contains unknown fields: %v", unknown)
	}

	providerID, _ := manifest["id"].(string)
	if !identifierPattern.MatchString(providerID) {
		return empty, fmt.Errorf("invalid provider id in %s", path)
	}

	loadedIDs := map[string]bool{}
	for _, loaded := range alreadyLoaded {
		loadedIDs[loaded.ID] = true
	}
	if loadedIDs[providerID] {
		return empty, fmt.Errorf("duplicate provider id: %s", providerID)
	}

	providerVersion := asString(manifest["version"])
	version, err := SemverTuple(providerVersion)
	if err != nil {
		return empty, err
	}

	compatibility, isObject := manifest["kernel_compatibility"].(map[string]any)
	if !isObject {
		return empty, fmt.Errorf("provider %s is missing kernel_compatibility", providerID)
	}
	minimum := asString(compatibility["minimum"])
	maximumExclusive := asString(compatibility["maximum_exclusive"])
	satisfied, err := VersionSatisfies(KernelVersion, minimum, maximumExclusive)
	if err != nil {
		return empty, err
	}
	if !satisfied {
		// The Python named the provider's own version here, which is not what
		// the check looks at -- it reported "provider cadre version 0.3.0 is
		// incompatible with kernel 0.13.0" while the range that actually
		// failed was [0.13.2, 1.0.0). Naming the range makes the message
		// point at the thing a reader has to change.
		return empty, fmt.Errorf(
			"provider %s requires kernel [%s, %s) but this kernel is %s; "+
				"install a provider compatible with this kernel",
			providerID, minimum, maximumExclusive, KernelVersion)
	}

	var dependencies []map[string]any
	for _, entry := range asSlice(manifest["dependencies"]) {
		dependency, isObject := entry.(map[string]any)
		if !isObject {
			return empty, fmt.Errorf("provider %s has malformed dependency metadata", providerID)
		}
		dependencyID, isText := dependency["id"].(string)
		if !isText {
			return empty, fmt.Errorf("provider %s has malformed dependency metadata", providerID)
		}
		if !loadedIDs[dependencyID] {
			return empty, fmt.Errorf("provider %s requires provider %s to be loaded first", providerID, dependencyID)
		}
		dependencies = append(dependencies, dependency)
	}

	root := filepath.Dir(path)
	catalogPath, err := ProviderResource(root, manifest["agent_catalog"], "agent_catalog", false)
	if err != nil {
		return empty, err
	}
	catalogData, err := loadJSONObject(catalogPath)
	if err != nil {
		return empty, err
	}
	agentsRaw, hasAgents := catalogData["agents"].(map[string]any)
	if asFloat(catalogData["schema_version"]) != 1 || !hasAgents {
		return empty, fmt.Errorf("provider %s agent catalog must contain an agents object", providerID)
	}

	agents := map[string]map[string]any{}
	for agentID, entry := range agentsRaw {
		agent, isObject := entry.(map[string]any)
		if !identifierPattern.MatchString(agentID) || !isObject {
			return empty, fmt.Errorf("provider %s has an invalid agent id: %s", providerID, agentID)
		}
		kind := asString(agent["kind"])
		if !validAgentKinds[kind] {
			return empty, fmt.Errorf("provider %s agent %s has unknown kind", providerID, agentID)
		}
		capabilities, err := stringList(agent["capabilities"])
		if err != nil {
			return empty, fmt.Errorf("provider %s agent %s has unknown capabilities", providerID, agentID)
		}
		for _, capability := range capabilities {
			if !validAgentCapabilities[capability] {
				return empty, fmt.Errorf("provider %s agent %s has unknown capabilities", providerID, agentID)
			}
		}
		// Separation of duties, enforced at load: a reviewer may carry no
		// capability other than "reviewer". An agent that both authors and
		// reviews is the one thing this whole model exists to prevent.
		if kind == "reviewer" {
			for _, capability := range capabilities {
				if capability != "reviewer" {
					return empty, fmt.Errorf("provider %s reviewer %s must remain read-only", providerID, agentID)
				}
			}
		}

		copied := map[string]any{}
		for key, value := range agent {
			copied[key] = value
		}
		if definition, isText := agent["definition"].(string); isText && definition != "" {
			resolved, err := ProviderResource(root, definition, "agent "+agentID+" definition", false)
			if err != nil {
				return empty, err
			}
			copied["definition"] = resolved
		}
		agents[agentID] = copied
	}

	profileRoots, err := resolveRoots(root, manifest["profile_roots"], "profile_roots")
	if err != nil {
		return empty, err
	}
	extensionRoots, err := resolveRoots(root, manifest["extension_roots"], "extension_roots")
	if err != nil {
		return empty, err
	}
	if len(profileRoots) == 0 {
		return empty, fmt.Errorf("provider %s must define at least one profile root", providerID)
	}

	if duplicates := intersect(childIDs(alreadyLoadedRoots(alreadyLoaded, true), "profile.json"),
		childIDs(profileRoots, "profile.json")); len(duplicates) > 0 {
		return empty, fmt.Errorf("provider %s duplicates profile ids: %v", providerID, duplicates)
	}
	if duplicates := intersect(childIDs(alreadyLoadedRoots(alreadyLoaded, false), "extension.json"),
		childIDs(extensionRoots, "extension.json")); len(duplicates) > 0 {
		return empty, fmt.Errorf("provider %s duplicates extension ids: %v", providerID, duplicates)
	}

	if err := checkProfiles(providerID, profileRoots); err != nil {
		return empty, err
	}
	if err := checkExtensions(providerID, extensionRoots); err != nil {
		return empty, err
	}

	manifestBytes, err := os.ReadFile(path)
	if err != nil {
		return empty, err
	}
	manifestDigest := sha256.Sum256(manifestBytes)
	catalogDigest, err := Fingerprint(catalogData)
	if err != nil {
		return empty, err
	}

	return LoadedProvider{
		ID:             providerID,
		Version:        fmt.Sprintf("%d.%d.%d", version[0], version[1], version[2]),
		ManifestSHA256: "sha256:" + hex.EncodeToString(manifestDigest[:]),
		CatalogSHA256:  catalogDigest,
		Dependencies:   dependencies,
		AgentCatalog:   agents,
		ProfileRoots:   profileRoots,
		ExtensionRoots: extensionRoots,
	}, nil
}

func resolveRoots(root string, value any, fieldName string) ([]string, error) {
	var roots []string
	for _, entry := range asSlice(value) {
		resolved, err := ProviderResource(root, entry, fieldName, true)
		if err != nil {
			return nil, err
		}
		roots = append(roots, resolved)
	}
	return roots, nil
}

func alreadyLoadedRoots(providers []LoadedProvider, profiles bool) []string {
	var roots []string
	for _, loaded := range providers {
		if profiles {
			roots = append(roots, loaded.ProfileRoots...)
		} else {
			roots = append(roots, loaded.ExtensionRoots...)
		}
	}
	return roots
}

// childIDs lists directory names under roots that hold the named manifest.
func childIDs(roots []string, manifestName string) map[string]bool {
	ids := map[string]bool{}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if info, err := os.Stat(filepath.Join(root, entry.Name(), manifestName)); err == nil && !info.IsDir() {
				ids[entry.Name()] = true
			}
		}
	}
	return ids
}

func intersect(left, right map[string]bool) []string {
	var shared []string
	for id := range left {
		if right[id] {
			shared = append(shared, id)
		}
	}
	sort.Strings(shared)
	return shared
}

func checkProfiles(providerID string, roots []string) error {
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			profilePath := filepath.Join(root, entry.Name(), "profile.json")
			if info, err := os.Stat(profilePath); err != nil || info.IsDir() {
				continue
			}
			profile, err := loadJSONObject(profilePath)
			if err != nil {
				return err
			}
			_, hasBindings := profile["gate_bindings"].(map[string]any)
			_, versionIsText := profile["version"].(string)
			if asString(profile["id"]) != entry.Name() || !versionIsText || !hasBindings {
				return fmt.Errorf("provider %s has malformed profile: %s", providerID, profilePath)
			}
		}
	}
	return nil
}

func checkExtensions(providerID string, roots []string) error {
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			extensionPath := filepath.Join(root, entry.Name(), "extension.json")
			if info, err := os.Stat(extensionPath); err != nil || info.IsDir() {
				continue
			}
			extension, err := loadJSONObject(extensionPath)
			if err != nil {
				return err
			}
			_, versionIsText := extension["version"].(string)
			if asFloat(extension["schema_version"]) != 1 || asString(extension["id"]) != entry.Name() || !versionIsText {
				return fmt.Errorf("provider %s has malformed extension: %s", providerID, extensionPath)
			}
		}
	}
	return nil
}

func asSlice(value any) []any {
	slice, _ := value.([]any)
	return slice
}

func asString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	if value == nil {
		return "None"
	}
	return fmt.Sprintf("%v", value)
}

func asFloat(value any) float64 {
	number, _ := value.(float64)
	return number
}

func stringList(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	slice, isSlice := value.([]any)
	if !isSlice {
		return nil, fmt.Errorf("not a list")
	}
	var values []string
	for _, entry := range slice {
		text, isText := entry.(string)
		if !isText {
			return nil, fmt.Errorf("not a string")
		}
		values = append(values, text)
	}
	return values, nil
}
