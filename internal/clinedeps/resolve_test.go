package clinedeps

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func checkoutRoot(t *testing.T) string {
	t.Helper()
	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Dir(filepath.Dir(working))
}

func realDeclarations(t *testing.T) map[string]map[string]string {
	t.Helper()
	declarations, err := WorkspaceRuntimeDependencies(checkoutRoot(t), Entrypoints)
	if err != nil {
		t.Fatalf("deriving the workspace closure: %v", err)
	}
	if len(declarations) == 0 {
		t.Fatal("no plugin-owned runtime dependencies found in cline-plugins/*/package.json; " +
			"every derivation below would vacuously pass")
	}
	// Checked first so a future range dependency fails with the clear message
	// rather than surfacing as the resolver's confusing "absent from both
	// shapes checked".
	if err := RefuseInexactPins(declarations); err != nil {
		t.Fatalf("a declared runtime dependency is not an exact pin: %v", err)
	}
	return declarations
}

func pointerTo(text string) *string { return &text }

func TestEveryDeclaredRuntimeDependencyIsAnExactPin(t *testing.T) {
	// Stated as its own test, not only as a precondition of the others, so
	// the assertion has a named outcome rather than failing as a setup error
	// attributed to whichever test happened to run first.
	realDeclarations(t)
}

func TestASemverRangeIsRefusedWithAClearMessage(t *testing.T) {
	// Before this refusal existed, a declared range sailed into the resolver,
	// whose exact-match comparison would never match, fall through to the
	// workspace-scoped keys, find nothing, and raise an "absent" error that
	// mentions neither ranges nor exact pins.
	err := RefuseInexactPins(map[string]map[string]string{
		"zod": {"cline-agents": ">=4.0.0 <5.0.0"},
	})
	if err == nil {
		t.Fatal("a semver range was accepted as an exact pin")
	}
	message := err.Error()
	for _, expected := range []string{"cline-agents", "zod", ">=4.0.0 <5.0.0",
		"not an exact", "range evaluator"} {
		if !strings.Contains(message, expected) {
			t.Errorf("the refusal does not mention %q:\n%s", expected, message)
		}
	}
	if strings.Contains(message, "absent from") {
		t.Errorf("the refusal reads as the downstream not-found failure:\n%s", message)
	}
}

func TestATrailingNewlineIsRefusedRatherThanSlippingThrough(t *testing.T) {
	// The pattern ends \z, not $. In Go as in Python, $ without the multiline
	// flag still matches before a trailing newline, so "4.4.3\n" would read as
	// an exact pin.
	if err := RefuseInexactPins(map[string]map[string]string{
		"zod": {"cline-agents": "4.4.3\n"},
	}); err == nil {
		t.Error("a version with a trailing newline was accepted as an exact pin")
	}
}

func TestWorkspacesAgreeOnEverySharedRuntimeDependency(t *testing.T) {
	for name, byWorkspace := range realDeclarations(t) {
		versions := map[string]bool{}
		for _, version := range byWorkspace {
			versions[version] = true
		}
		if len(versions) != 1 {
			t.Errorf("cline-plugins workspaces pin conflicting %s versions: %v. "+
				"The root closure can only carry one, so a Git-source install would "+
				"ship the wrong version to at least one entrypoint.", name, byWorkspace)
		}
	}
}

type rootManifest struct {
	Private      bool              `json:"private"`
	Dependencies map[string]string `json:"dependencies"`
	Workspaces   json.RawMessage   `json:"workspaces"`
	DevDeps      json.RawMessage   `json:"devDependencies"`
	Cline        struct {
		Plugins []struct {
			Paths []string `json:"paths"`
		} `json:"plugins"`
	} `json:"cline"`
}

func TestTheRootManifestDeclaresExactlyTheDiscoveredEntrypoints(t *testing.T) {
	root := checkoutRoot(t)
	var manifest rootManifest
	if err := ReadJSON(root, &manifest, "package.json"); err != nil {
		t.Fatalf("reading package.json: %v", err)
	}
	if !manifest.Private {
		t.Error("the root package.json is not private, so it could be published by accident")
	}
	if manifest.Workspaces != nil {
		t.Error("the root manifest declares workspaces. A Git-source install resolves " +
			"from the root node_modules, and a workspace layout there changes what npm builds.")
	}
	if manifest.DevDeps != nil {
		t.Error("the root manifest declares devDependencies. The root closure is what a " +
			"Git-source install ships; it should carry runtime packages only.")
	}
	if len(manifest.Cline.Plugins) != 1 ||
		strings.Join(manifest.Cline.Plugins[0].Paths, "\n") != strings.Join(Entrypoints, "\n") {
		t.Errorf("the root manifest's cline.plugins is %v, not exactly one entry listing %v",
			manifest.Cline.Plugins, Entrypoints)
	}
	for _, entrypoint := range Entrypoints {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(entrypoint, "./")))); err != nil {
			t.Errorf("declared entrypoint %s does not exist: %v", entrypoint, err)
		}
	}
}

func TestTheRootManifestMatchesTheWorkspaceRuntimeDependencies(t *testing.T) {
	declarations := realDeclarations(t)
	expected := map[string]string{}
	for name, byWorkspace := range declarations {
		for _, workspace := range sortedKeys(byWorkspace) {
			expected[name] = byWorkspace[workspace]
			break
		}
	}
	var manifest rootManifest
	if err := ReadJSON(checkoutRoot(t), &manifest, "package.json"); err != nil {
		t.Fatalf("reading package.json: %v", err)
	}
	for name, version := range expected {
		if manifest.Dependencies[name] != version {
			t.Errorf("root package.json declares %s as %q, cline-plugins declares %q. "+
				"Bump both sides together: the root closure is what a `cline plugin "+
				"install <git-url>` actually resolves.",
				name, manifest.Dependencies[name], version)
		}
	}
	for name := range manifest.Dependencies {
		if _, declared := expected[name]; !declared {
			t.Errorf("root package.json declares %s, which no cline-plugins workspace "+
				"asks for", name)
		}
	}
}

func TestTheRootLockfilePinsTheRuntimeClosure(t *testing.T) {
	root := checkoutRoot(t)
	var manifest rootManifest
	if err := ReadJSON(root, &manifest, "package.json"); err != nil {
		t.Fatalf("reading package.json: %v", err)
	}
	var lockfile struct {
		LockfileVersion int `json:"lockfileVersion"`
		Packages        map[string]struct {
			Version      string            `json:"version"`
			Dependencies map[string]string `json:"dependencies"`
		} `json:"packages"`
	}
	if err := ReadJSON(root, &lockfile, "package-lock.json"); err != nil {
		t.Fatalf("reading package-lock.json: %v", err)
	}
	if lockfile.LockfileVersion != 3 {
		t.Errorf("root lockfileVersion is %d, not 3", lockfile.LockfileVersion)
	}
	for name, version := range manifest.Dependencies {
		if lockfile.Packages[""].Dependencies[name] != version {
			t.Errorf("the root lockfile's own dependency record disagrees with "+
				"package.json on %s", name)
		}
		if got := lockfile.Packages["node_modules/"+name].Version; got != version {
			t.Errorf("the root lockfile resolves %s to %q, not the declared %q",
				name, got, version)
		}
	}
}

func TestBothLockfilesResolveTheSameRuntimeVersions(t *testing.T) {
	// This comparison is the only check tying the root lockfile to
	// cline-plugins/ at all: the cline-git-source CI job proves resolvability
	// but not version, and the release SBOM scans only cline-plugins/.
	root := checkoutRoot(t)
	declarations := realDeclarations(t)
	var rootLock, workspaceLock Lockfile
	if err := ReadJSON(root, &rootLock, "package-lock.json"); err != nil {
		t.Fatalf("reading the root lockfile: %v", err)
	}
	if err := ReadJSON(root, &workspaceLock, "cline-plugins", "package-lock.json"); err != nil {
		t.Fatalf("reading the workspace lockfile: %v", err)
	}
	for _, dependency := range sortedKeys(declarations) {
		key, err := ResolveWorkspacePackageKey(workspaceLock.Packages, dependency, declarations[dependency])
		if err != nil {
			t.Errorf("%s: %v", dependency, err)
			continue
		}
		rootKey := "node_modules/" + dependency
		rootEntry, present := rootLock.Packages[rootKey]
		if !present {
			// Said explicitly rather than read as a zero value, which would
			// send a reader looking for a resolver bug instead of a missing
			// dependency.
			t.Errorf("%s is declared by a cline-plugins workspace but absent from the "+
				"root package-lock.json. A Git-source install ships the root closure, "+
				"so the dependency would be missing at runtime there.", dependency)
			continue
		}
		workspaceEntry := workspaceLock.Packages[key]
		if rootEntry.Version != workspaceEntry.Version {
			t.Errorf("%s resolves to %s in the root lockfile and %s in "+
				"cline-plugins/package-lock.json. CI tests the latter; a Git-source "+
				"install ships the former.", dependency, rootEntry.Version, workspaceEntry.Version)
			continue
		}
		if identityOf(rootEntry) != identityOf(workspaceEntry) {
			t.Errorf("%s resolves to the same version in both lockfiles but a different "+
				"artifact. Same version, different tarball is a substitution, not "+
				"drift -- treat it as such.", dependency)
		}
	}
}

func TestADeeperEntrypointNamesTheDerivationItBreaks(t *testing.T) {
	// The workspace name is derived from the directory layout. An entrypoint
	// one level deeper resolves to the wrong directory, and that used to
	// surface as a file-not-found naming a path nobody expected rather than
	// the assumption that produced it.
	_, err := WorkspaceRuntimeDependencies(checkoutRoot(t),
		[]string{"./cline-plugins/cline/dist/index.ts"})
	if err == nil {
		t.Fatal("a deeper entrypoint was accepted, silently targeting the wrong workspace")
	}
	for _, expected := range []string{"dist", "parent directory", "directly under"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the error does not mention %q:\n%s", expected, err)
		}
	}
}

func TestTheRealEntrypointsStillResolve(t *testing.T) {
	// The guard must not fire on the layout as it exists today.
	root := checkoutRoot(t)
	for _, byWorkspace := range realDeclarations(t) {
		for workspace := range byWorkspace {
			path := filepath.Join(root, "cline-plugins", workspace, "package.json")
			if _, err := os.Stat(path); err != nil {
				t.Errorf("derived workspace %q has no package.json: %v", workspace, err)
			}
		}
	}
}

func TestAValidTopLevelEntryDoesNotHideADisagreeingWorkspaceCopy(t *testing.T) {
	// Node resolves from the closest node_modules upward, so a workspace
	// holding its own copy loads that one even when a correctly pinned copy
	// also sits at the top level. Returning the top-level key without checking
	// for a disagreeing sibling would pass a tree whose workspaces genuinely
	// load different versions.
	_, err := ResolveWorkspacePackageKey(map[string]LockPackage{
		"node_modules/zod":              {Version: "4.4.3", Integrity: pointerTo("sha512-ours")},
		"cline-agents/node_modules/zod": {Version: "6.6.6", Integrity: pointerTo("sha512-other")},
	}, "zod", map[string]string{"cline": "4.4.3", "cline-agents": "4.4.3"})
	if err == nil {
		t.Fatal("a disagreeing workspace copy was hidden by a valid top-level entry")
	}
	for _, expected := range []string{"cline-agents/node_modules/zod", "6.6.6", "only resolution"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the error does not mention %q:\n%s", expected, err)
		}
	}
}

func TestAWorkspaceCopyMatchingTheTopLevelEntryIsNotAConflict(t *testing.T) {
	// npm can legitimately write both keys at the same version. Only a
	// disagreeing sibling is drift; an identical one is duplication, and
	// failing on it would make the check reject correct trees.
	key, err := ResolveWorkspacePackageKey(map[string]LockPackage{
		"node_modules/zod":              {Version: "4.4.3", Integrity: pointerTo("sha512-ours")},
		"cline-agents/node_modules/zod": {Version: "4.4.3", Integrity: pointerTo("sha512-ours")},
	}, "zod", map[string]string{"cline": "4.4.3", "cline-agents": "4.4.3"})
	if err != nil {
		t.Fatalf("an identical duplicate was rejected: %v", err)
	}
	if key != "node_modules/zod" {
		t.Errorf("resolved to %q, not the top-level key", key)
	}
}

func TestASiblingWithoutAnIntegrityFieldIsNotACrash(t *testing.T) {
	// A git- or file:-sourced entry has no integrity field. Missing is treated
	// as a distinct value, so a registry copy and a non-registry copy read as
	// disagreeing rather than crashing or being waved through.
	_, err := ResolveWorkspacePackageKey(map[string]LockPackage{
		"node_modules/zod":              {Version: "4.4.3", Integrity: pointerTo("sha512-ours")},
		"cline-agents/node_modules/zod": {Version: "4.4.3"},
	}, "zod", map[string]string{"cline": "4.4.3", "cline-agents": "4.4.3"})
	if err == nil {
		t.Fatal("a sibling with no integrity field was treated as identical")
	}
	if !strings.Contains(err.Error(), "cline-agents/node_modules/zod") {
		t.Errorf("the error does not name the sibling:\n%s", err)
	}
}

func TestTheFallbackPathAlsoSurvivesAMissingIntegrityField(t *testing.T) {
	// New here, and the reason it is: the Python compared integrity with a
	// direct index on this path while using a safe lookup on the top-level
	// one. Two declaring workspaces both pushed down by hoisting, one of them
	// git-sourced, raised a bare KeyError -- the same defect the safe lookup
	// was written to fix, still live on the other branch of the same function,
	// covered by no test.
	//
	// The two siblings disagree (one has integrity, one does not), so the
	// fail-closed answer is a refusal naming both, not a crash and not a
	// silent pick.
	_, err := ResolveWorkspacePackageKey(map[string]LockPackage{
		"node_modules/zod":              {Version: "9.9.9", Integrity: pointerTo("sha512-unrelated")},
		"cline/node_modules/zod":        {Version: "4.4.3", Integrity: pointerTo("sha512-ours")},
		"cline-agents/node_modules/zod": {Version: "4.4.3"},
	}, "zod", map[string]string{"cline": "4.4.3", "cline-agents": "4.4.3"})
	if err == nil {
		t.Fatal("disagreeing fallback candidates were silently reduced to one")
	}
	for _, expected := range []string{"cline/node_modules/zod", "cline-agents/node_modules/zod",
		"no integrity field", "Refusing to silently pick one"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("the refusal does not mention %q:\n%s", expected, err)
		}
	}
}

func TestTheHoistingFallbackDoesNotMatchAnUnrelatedTransitiveDependant(t *testing.T) {
	// zod is declared by the cline workspace, but npm hoisted a *different*
	// package's zod pin into the top-level slot, pushing cline's own copy down.
	// An unrelated transitive dependant also carries its own nested zod -- a
	// leaf-name match that must never be confused for one of our workspaces'.
	key, err := ResolveWorkspacePackageKey(map[string]LockPackage{
		"node_modules/zod": {Version: "4.0.0", Integrity: pointerTo("sha512-unrelated")},
		"node_modules/dify-ai-provider/node_modules/zod": {Version: "3.25.76", Integrity: pointerTo("sha512-dify-own-pin")},
		"cline/node_modules/zod":                         {Version: "3.24.1", Integrity: pointerTo("sha512-clines-own-pin")},
	}, "zod", map[string]string{"cline": "^3.24.1"})
	if err != nil {
		t.Fatalf("the fallback failed on a legitimately hoisted tree: %v", err)
	}
	if key != "cline/node_modules/zod" {
		t.Errorf("resolved to %q; the fallback must not match an unrelated transitive "+
			"dependant's own nested pin just because the leaf package name matches", key)
	}
}

func TestTheHoistingFallbackFailsLoudlyOnDisagreeingCandidates(t *testing.T) {
	// If more than one of our own workspaces declares the dependency and npm
	// hoisted neither pin to the top-level slot, the fallback must refuse to
	// pick one rather than mask real drift between them.
	if _, err := ResolveWorkspacePackageKey(map[string]LockPackage{
		"node_modules/zod":              {Version: "4.0.0", Integrity: pointerTo("sha512-unrelated")},
		"cline/node_modules/zod":        {Version: "3.24.1", Integrity: pointerTo("sha512-cline-pin")},
		"cline-agents/node_modules/zod": {Version: "3.24.2", Integrity: pointerTo("sha512-cline-agents-pin")},
	}, "zod", map[string]string{"cline": "^3.24.1", "cline-agents": "^3.24.2"}); err == nil {
		t.Error("disagreeing fallback candidates were silently reduced to one")
	}
}

func TestTheClaudeAndCodexMarketplacePackageRemainsNpmFree(t *testing.T) {
	root := filepath.Join(checkoutRoot(t), "plugin")
	var found []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if info.Name() != "package.json" || strings.Contains(path, "node_modules") {
			return nil
		}
		relative, _ := filepath.Rel(checkoutRoot(t), path)
		found = append(found, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		t.Fatalf("walking plugin/: %v", err)
	}
	if len(found) > 0 {
		t.Errorf("the Claude/Codex marketplace package has picked up npm manifests: %v. "+
			"It is meant to be installable without a node toolchain.", found)
	}
}
