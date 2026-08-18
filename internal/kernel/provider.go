package kernel

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
)

// Provider loading: how a kernel learns about profiles, extensions and an
// agent catalog it did not ship with.
//
// A port of load_provider and the introspection subcommands (provider,
// profile, extension). Everything here is read-only -- nothing is written,
// nothing is approved -- but it decides what the rest of the kernel will
// treat as installed, so the refusals are the substance.
//
// Two of them are worth naming up front. A provider whose declared kernel
// compatibility does not include this kernel is refused rather than loaded
// hopefully: a profile written against different gate semantics is not
// "mostly fine". And a catalog agent declared `reviewer` may hold no
// capability beyond reviewing -- authorship/approval separation is not a
// policy the kernel applies afterwards, it is a property of what it will
// accept as a catalog at all.

// Version is the kernel version providers declare compatibility against.
//
// A literal, guarded rather than derived: reading it from a file at runtime
// would make the version depend on a checkout being present. It was guarded
// against the Python kernel's own literal until that kernel was deleted;
// TestTheKernelVersionIsInsideEveryProviderCompatibilityWindow now guards it
// against the provider bundles this repository ships, which is the thing it
// has to agree with for a load to succeed.
//
// It is not decoration. Providers declare a kernel_compatibility range and
// are refused outside it, so a wrong version here either rejects a provider
// that should load or accepts one written for different gate semantics.
// 0.14.0 rather than 0.13.x: every kernel-v0.13.* tag was cut from the Python
// kernel, which #317 deleted. The Go kernel claiming 0.13.2 meant
// kernel-publish's "skip if already tagged" check would skip forever -- the
// version it wanted to publish was already a tag, naming a different
// implementation's bits. A minor bump both frees the namespace and says the
// implementation changed. TestTheKernelVersionIsNotAlreadyTagged holds it.
const Version = "0.14.0"

var (
	providerIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	semverPattern     = regexp.MustCompile(`^([0-9]+)\.([0-9]+)\.([0-9]+)$`)

	allowedManifestKeys = map[string]bool{
		"schema_version": true, "id": true, "version": true, "kernel_compatibility": true,
		"agent_catalog": true, "profile_roots": true, "extension_roots": true,
		"dependencies": true, "dispatch_bindings": true,
	}
	validAgentKinds        = map[string]bool{"author": true, "reviewer": true, "specialist": true}
	validAgentCapabilities = map[string]bool{"author": true, "reviewer": true, "dispatch": true}
)

// Registry is the loaded provider state for one kernel invocation.
//
// Explicit rather than package-level globals, which is what the Python
// kernel uses. Globals make load order invisible, and load order is
// load-bearing here: a provider may depend on one already loaded, and
// duplicate detection compares against what came before.
// LoadedProvider is what the kernel records about a provider it accepted.
//
// Not the manifest. `provider inspect` prints this, and the difference
// matters: the two digests are integrity evidence, binding the manifest bytes
// and the catalog's canonical content to what was loaded. A run record citing
// a provider cites these, so reprinting the manifest instead would replace
// evidence with a copy of the input.
//
// Field order is the Python dict's insertion order, which is what json.dumps
// emits and what the differential compares.
type LoadedProvider struct {
	ID             string `json:"id"`
	Version        string `json:"version"`
	ManifestSHA256 string `json:"manifest_sha256"`
	CatalogSHA256  string `json:"catalog_sha256"`
	Dependencies   []any  `json:"dependencies"`
}

type Registry struct {
	Providers       []LoadedProvider
	ProfileRoots    []string
	ExtensionRoots  []string
	CatalogRoots    []string
	kernelVersionOf string
}

// NewRegistry returns an empty registry for this kernel version.
func NewRegistry() *Registry {
	return &Registry{kernelVersionOf: Version}
}

// LoadProvider validates and registers one provider manifest.
func (r *Registry) LoadProvider(manifestPath string) error {
	path, err := filepath.Abs(manifestPath)
	if err != nil {
		return fmt.Errorf("cannot resolve provider manifest path %s: %w", manifestPath, err)
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}

	manifest, err := loadJSONObject(path)
	if err != nil {
		return err
	}

	if version, _ := jsonNumber(manifest["schema_version"]); version != 1 {
		return fmt.Errorf("unsupported provider schema in %s", path)
	}
	var unknown []string
	for key := range manifest {
		if !allowedManifestKeys[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("provider manifest contains unknown fields: %v", unknown)
	}

	providerID, _ := manifest["id"].(string)
	if !providerIDPattern.MatchString(providerID) {
		return fmt.Errorf("invalid provider id in %s", path)
	}
	if r.isLoaded(providerID) {
		return fmt.Errorf("duplicate provider id: %s", providerID)
	}

	version, err := semverTuple(fmt.Sprint(manifest["version"]))
	if err != nil {
		return err
	}

	compatibility, ok := manifest["kernel_compatibility"].(map[string]any)
	if !ok {
		return fmt.Errorf("provider %s is missing kernel_compatibility", providerID)
	}
	minimumText := fmt.Sprint(compatibility["minimum"])
	maximumText := fmt.Sprint(compatibility["maximum_exclusive"])
	minimum, err := semverTuple(minimumText)
	if err != nil {
		return err
	}
	maximum, err := semverTuple(maximumText)
	if err != nil {
		return err
	}
	current, err := semverTuple(r.kernelVersionOf)
	if err != nil {
		return err
	}
	// Refused rather than loaded hopefully. A provider declares the kernel
	// range whose gate semantics it was written against; outside that range
	// its profiles describe a lifecycle this kernel does not implement.
	if semverLessThan(current, minimum) || !semverLessThan(current, maximum) {
		return fmt.Errorf(
			"provider %s declares kernel_compatibility [%s, %s), which does not include this "+
				"kernel's version %s; install a provider compatible with kernel %s, or a kernel "+
				"version within the provider's declared range",
			providerID, minimumText, maximumText, r.kernelVersionOf, r.kernelVersionOf)
	}

	// Dependencies must already be loaded. Order is the caller's to get
	// right; resolving it here would mean loading a provider before whatever
	// it builds on has been validated.
	if dependencies, present := manifest["dependencies"]; present {
		list, ok := dependencies.([]any)
		if !ok {
			return fmt.Errorf("provider %s has malformed dependency metadata", providerID)
		}
		for _, raw := range list {
			dependency, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("provider %s has malformed dependency metadata", providerID)
			}
			dependencyID, ok := dependency["id"].(string)
			if !ok {
				return fmt.Errorf("provider %s has malformed dependency metadata", providerID)
			}
			if !r.isLoaded(dependencyID) {
				return fmt.Errorf(
					"provider %s requires provider %s to be loaded first", providerID, dependencyID)
			}
		}
	}

	root := filepath.Dir(path)
	catalogPath, err := providerResource(root, manifest["agent_catalog"], "agent_catalog", false)
	if err != nil {
		return err
	}
	if err := validateAgentCatalog(providerID, catalogPath); err != nil {
		return err
	}

	profileRoots, err := r.resolveRoots(root, manifest["profile_roots"], "profile_roots")
	if err != nil {
		return err
	}
	extensionRoots, err := r.resolveRoots(root, manifest["extension_roots"], "extension_roots")
	if err != nil {
		return err
	}
	if len(profileRoots) == 0 {
		return fmt.Errorf("provider %s must define at least one profile root", providerID)
	}

	// Duplicate profile ids across providers are refused: two providers
	// claiming the same profile id means the lifecycle a project gets depends
	// on load order, which is not something a project can see.
	existing := r.ProfileIDs()
	supplied := idsUnder(profileRoots, "profile.json")
	var duplicates []string
	for id := range supplied {
		if existing[id] {
			duplicates = append(duplicates, id)
		}
	}
	if len(duplicates) > 0 {
		sort.Strings(duplicates)
		return fmt.Errorf("provider %s supplies duplicate profiles: %v", providerID, duplicates)
	}

	// Profiles and extensions this provider supplies are validated before it
	// is accepted. A provider whose profile declares a different id than its
	// directory would make the lifecycle a project resolves depend on which
	// of the two the reader believed.
	if err := validateSuppliedProfiles(providerID, profileRoots); err != nil {
		return err
	}
	if err := validateSuppliedExtensions(providerID, extensionRoots); err != nil {
		return err
	}

	manifestBytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	catalogData, err := loadJSONObject(catalogPath)
	if err != nil {
		return err
	}
	catalogFingerprint, err := fingerprint(catalogData)
	if err != nil {
		return err
	}
	dependencies, _ := manifest["dependencies"].([]any)
	if dependencies == nil {
		dependencies = []any{}
	}
	r.Providers = append(r.Providers, LoadedProvider{
		ID: providerID,
		// The normalised semver, not the string as written: "01.2.3" and
		// "1.2.3" name the same version, and recording them differently would
		// make two identical providers look distinct in a run record.
		Version:        fmt.Sprintf("%d.%d.%d", version[0], version[1], version[2]),
		ManifestSHA256: "sha256:" + hexSHA256(manifestBytes),
		CatalogSHA256:  catalogFingerprint,
		Dependencies:   dependencies,
	})
	r.ProfileRoots = append(r.ProfileRoots, profileRoots...)
	r.ExtensionRoots = append(r.ExtensionRoots, extensionRoots...)
	r.CatalogRoots = append(r.CatalogRoots, catalogPath)
	return nil
}

func (r *Registry) isLoaded(id string) bool {
	for _, loaded := range r.Providers {
		if loaded.ID == id {
			return true
		}
	}
	return false
}

func (r *Registry) resolveRoots(root string, value any, field string) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("provider %s must be a list", field)
	}
	var roots []string
	for _, item := range list {
		resolved, err := providerResource(root, item, field, true)
		if err != nil {
			return nil, err
		}
		roots = append(roots, resolved)
	}
	return roots, nil
}

// ProfileIDs are the profile ids visible across every loaded provider.
func (r *Registry) ProfileIDs() map[string]bool { return idsUnder(r.ProfileRoots, "profile.json") }

// ExtensionIDs are the extension ids visible across every loaded provider.
func (r *Registry) ExtensionIDs() map[string]bool {
	return idsUnder(r.ExtensionRoots, "extension.json")
}

// idsUnder lists the directory names under roots that contain marker.
func idsUnder(roots []string, marker string) map[string]bool {
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
			if info, err := os.Stat(filepath.Join(root, entry.Name(), marker)); err == nil && info.Mode().IsRegular() {
				ids[entry.Name()] = true
			}
		}
	}
	return ids
}

// providerResource resolves a manifest-relative path and refuses one that
// escapes the manifest's own directory.
//
// A manifest is data the kernel was pointed at, not code it wrote. Without
// this, `"agent_catalog": "../../../etc/passwd"` is a manifest that makes the
// kernel read whatever the caller names.
func providerResource(root string, value any, field string, directory bool) (string, error) {
	text, ok := value.(string)
	if !ok || text == "" {
		return "", fmt.Errorf("provider %s must be a non-empty relative path", field)
	}
	candidate := filepath.Join(root, text)
	absolute, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("provider %s cannot be resolved: %w", field, err)
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		absolute = resolved
	}
	rootAbsolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(rootAbsolute); err == nil {
		rootAbsolute = resolved
	}
	relative, err := filepath.Rel(rootAbsolute, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("provider %s escapes its manifest directory", field)
	}

	info, err := os.Stat(absolute)
	if directory {
		if err != nil || !info.IsDir() {
			return "", fmt.Errorf("provider %s directory does not exist: %s", field, text)
		}
	} else {
		if err != nil || !info.Mode().IsRegular() {
			return "", fmt.Errorf("provider %s file does not exist: %s", field, text)
		}
	}
	return absolute, nil
}

// validateAgentCatalog refuses a catalog the kernel will not stand behind.
//
// The reviewer rule is the one that matters: an agent declared `reviewer` may
// hold no capability beyond reviewing. A reviewer that could also author is
// an identity able to approve its own work, and that separation is structural
// across this whole system rather than a rule applied later.
func validateAgentCatalog(providerID, catalogPath string) error {
	catalog, err := loadJSONObject(catalogPath)
	if err != nil {
		return err
	}
	if version, _ := jsonNumber(catalog["schema_version"]); version != 1 {
		return fmt.Errorf("provider %s agent catalog must contain an agents object", providerID)
	}
	agents, ok := catalog["agents"].(map[string]any)
	if !ok {
		return fmt.Errorf("provider %s agent catalog must contain an agents object", providerID)
	}

	for agentID, raw := range agents {
		agent, ok := raw.(map[string]any)
		if !ok || !providerIDPattern.MatchString(agentID) {
			return fmt.Errorf("provider %s has an invalid agent id: %s", providerID, agentID)
		}
		kind, _ := agent["kind"].(string)
		if !validAgentKinds[kind] {
			return fmt.Errorf("provider %s agent %s has unknown kind", providerID, agentID)
		}
		capabilities, err := stringList(agent["capabilities"])
		if err != nil {
			return fmt.Errorf("provider %s agent %s has unknown capabilities", providerID, agentID)
		}
		for _, capability := range capabilities {
			if !validAgentCapabilities[capability] {
				return fmt.Errorf("provider %s agent %s has unknown capabilities", providerID, agentID)
			}
		}
		if kind == "reviewer" {
			for _, capability := range capabilities {
				if capability != "reviewer" {
					return fmt.Errorf(
						"provider %s reviewer %s must remain read-only", providerID, agentID)
				}
			}
		}
	}
	return nil
}

func stringList(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("not a list")
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		text, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("not a list of strings")
		}
		out = append(out, text)
	}
	return out, nil
}

func loadJSONObject(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return object, nil
}

func jsonNumber(value any) (int, bool) {
	switch typed := value.(type) {
	case float64:
		return int(typed), true
	case int:
		return typed, true
	}
	return 0, false
}

func semverTuple(value string) ([3]int, error) {
	match := semverPattern.FindStringSubmatch(value)
	if match == nil {
		return [3]int{}, fmt.Errorf("invalid semantic version: %s", value)
	}
	var parsed [3]int
	for index := 0; index < 3; index++ {
		number, err := strconv.Atoi(match[index+1])
		if err != nil {
			return [3]int{}, fmt.Errorf("invalid semantic version: %s", value)
		}
		parsed[index] = number
	}
	return parsed, nil
}

func semverLessThan(a, b [3]int) bool {
	for index := 0; index < 3; index++ {
		if a[index] != b[index] {
			return a[index] < b[index]
		}
	}
	return false
}

// validateSuppliedProfiles refuses a profile whose declared id disagrees with
// its directory, or which is missing the fields a profile is made of.
func validateSuppliedProfiles(providerID string, roots []string) error {
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			path := filepath.Join(root, entry.Name(), "profile.json")
			if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
				continue
			}
			profile, err := loadJSONObject(path)
			if err != nil {
				return err
			}
			id, _ := profile["id"].(string)
			profileVersion, versionOK := profile["version"].(string)
			_, bindingsOK := profile["gate_bindings"].(map[string]any)
			if id != entry.Name() || !versionOK || profileVersion == "" && false || !bindingsOK {
				return fmt.Errorf("provider %s has malformed profile: %s", providerID, path)
			}
		}
	}
	return nil
}

// validateSuppliedExtensions applies the same rule to extensions.
func validateSuppliedExtensions(providerID string, roots []string) error {
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			path := filepath.Join(root, entry.Name(), "extension.json")
			if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
				continue
			}
			extension, err := loadJSONObject(path)
			if err != nil {
				return err
			}
			schemaVersion, _ := jsonNumber(extension["schema_version"])
			id, _ := extension["id"].(string)
			_, versionOK := extension["version"].(string)
			if schemaVersion != 1 || id != entry.Name() || !versionOK {
				return fmt.Errorf("provider %s has malformed extension: %s", providerID, path)
			}
		}
	}
	return nil
}

// fingerprint is the kernel's canonical content hash: JSON with sorted keys
// and no whitespace, then sha256.
//
// Canonical rather than raw bytes, so two files that say the same thing
// formatted differently produce the same fingerprint -- which is what makes
// it usable as evidence that a catalog's *content* has not changed.
// fingerprint delegates to the shared implementation rather than keeping its
// own.
//
// It used to encode with encoding/json and SetEscapeHTML(false), which is the
// same canonical form as Python's for ASCII and a different one otherwise:
// json.dumps defaults to ensure_ascii=True, so "café" is six bytes on one side
// and ten on the other. Provider digests over any manifest containing a single
// accented character would have differed, silently, and the two kernels would
// have disagreed about whether a provider had changed.
func fingerprint(value any) (string, error) { return Fingerprint(value) }

func hexSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
