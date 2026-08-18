package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// install.sh and install.ps1 are the first thing a new user runs and the least
// likely thing to be exercised in normal development, so what is pinned here
// is the set of properties whose failure is silent or destructive.
//
// Ported from plugin/tools/test_install_script.py.

func repoFile(t *testing.T, parts ...string) string {
	t.Helper()
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	content, err := os.ReadFile(filepath.Join(append([]string{root}, parts...)...))
	if err != nil {
		t.Fatalf("cannot read %s: %v", filepath.Join(parts...), err)
	}
	return string(content)
}

func installScripts(t *testing.T) (posix, powershell string) {
	t.Helper()
	return repoFile(t, "install.sh"), repoFile(t, "install.ps1")
}

func TestThePosixInstallerIsExecutableAndPlainSh(t *testing.T) {
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	info, err := os.Stat(filepath.Join(root, "install.sh"))
	if err != nil {
		t.Fatalf("install.sh is missing: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Error("install.sh is not executable, so `./install.sh` fails for anyone " +
			"who follows the documented command")
	}
	// It runs on machines nobody has prepared; bashisms defeat that.
	if posix, _ := installScripts(t); !strings.HasPrefix(posix, "#!/bin/sh") {
		t.Error("install.sh does not start #!/bin/sh. It runs on machines nobody " +
			"has prepared, where bash may not exist.")
	}
}

// The two manifests spell `source` differently: Claude's is a plain string,
// Codex's is an object with a path. Kept as RawMessage so one reader serves
// both -- decoding it eagerly into either shape fails on the other, which is
// what the Python never noticed, since it only ever indexed into the Codex
// one.
type marketplaceManifest struct {
	Name    string `json:"name"`
	Plugins []struct {
		Name   string          `json:"name"`
		Source json.RawMessage `json:"source"`
	} `json:"plugins"`
}

func readMarketplace(t *testing.T, parts ...string) marketplaceManifest {
	t.Helper()
	var manifest marketplaceManifest
	if err := json.Unmarshal([]byte(repoFile(t, parts...)), &manifest); err != nil {
		t.Fatalf("%s does not parse: %v", filepath.Join(parts...), err)
	}
	if manifest.Name == "" {
		t.Fatalf("%s declares no marketplace name", filepath.Join(parts...))
	}
	return manifest
}

// codexSourcePath reads the object form Codex uses.
func codexSourcePath(t *testing.T, name string, raw json.RawMessage) string {
	t.Helper()
	var source struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Errorf("%s: Codex source is not an object with a path: %v", name, err)
		return ""
	}
	return source.Path
}

func TestBothMarketplacesAgreeOnTheirName(t *testing.T) {
	// Claude and Codex read different manifest paths. If they disagree on the
	// name, one runner's install silently targets a marketplace that does not
	// exist.
	claude := readMarketplace(t, ".claude-plugin", "marketplace.json")
	codex := readMarketplace(t, ".agents", "plugins", "marketplace.json")
	if claude.Name != codex.Name {
		t.Errorf("the Claude manifest calls the marketplace %q and the Codex one "+
			"calls it %q", claude.Name, codex.Name)
	}
}

func TestTheInstallersUseTheRealMarketplaceName(t *testing.T) {
	name := readMarketplace(t, ".claude-plugin", "marketplace.json").Name
	posix, powershell := installScripts(t)
	if !strings.Contains(posix, `MARKETPLACE="`+name+`"`) {
		t.Errorf("install.sh does not set MARKETPLACE to %q", name)
	}
	if !strings.Contains(powershell, `$Marketplace = '`+name+`'`) {
		t.Errorf("install.ps1 does not set $Marketplace to %q", name)
	}
}

func TestTheInstallersOnlyNamePluginsTheMarketplaceDeclares(t *testing.T) {
	// A plugin name that drifts from the manifest produces an installer that
	// fails at the last step, after already having changed things.
	claude := readMarketplace(t, ".claude-plugin", "marketplace.json")
	declared := map[string]bool{}
	for _, plugin := range claude.Plugins {
		declared[plugin.Name] = true
	}
	posix, powershell := installScripts(t)
	for _, name := range []string{"cadre", "cadre-lifecycle-core"} {
		if !declared[name] {
			t.Errorf("the marketplace does not declare a %q plugin, but the "+
				"installers ask for it", name)
		}
	}
	if !strings.Contains(posix, `PLUGIN="cadre"`) {
		t.Error(`install.sh does not set PLUGIN="cadre"`)
	}
	// The optional lifecycle plugin both scripts install with --with-lifecycle.
	for label, script := range map[string]string{"install.sh": posix, "install.ps1": powershell} {
		if !strings.Contains(script, "cadre-lifecycle-core") {
			t.Errorf("%s never names cadre-lifecycle-core, so --with-lifecycle "+
				"installs nothing", label)
		}
	}
}

func TestEveryCodexMarketplacePathResolves(t *testing.T) {
	// Codex reads its manifest from the repository root, so every source path
	// needs the plugin/ prefix the monorepo introduced. A stale ./ path
	// silently resolves to the repository root instead of the plugin.
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	codex := readMarketplace(t, ".agents", "plugins", "marketplace.json")
	if len(codex.Plugins) == 0 {
		t.Fatal("the Codex manifest declares no plugins; this guard checked nothing")
	}
	for _, plugin := range codex.Plugins {
		path := codexSourcePath(t, plugin.Name, plugin.Source)
		if path == "" {
			continue
		}
		if !strings.HasPrefix(path, "./plugin") {
			t.Errorf("%s: source path %q does not start ./plugin, so it resolves to "+
				"the repository root instead of the plugin", plugin.Name, path)
			continue
		}
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil || !info.IsDir() {
			t.Errorf("%s: source path %q does not resolve to a directory", plugin.Name, path)
		}
	}
}

// pinnedReleaseCoordinate matches the ways an installer can write a version
// down. Assembled from pieces so this file does not itself read as an example.
var pinnedReleaseCoordinate = regexp.MustCompile(
	`@v\d+\.\d+\.\d+|--branch\s+v\d|` + "clone" + `\s+--branch`)

func TestNeitherInstallerPinsAReleaseTag(t *testing.T) {
	// Same class as the pinned marketplace refs: installers must track the
	// default branch, because a written-down tag goes stale silently.
	posix, powershell := installScripts(t)
	for label, script := range map[string]string{"install.sh": posix, "install.ps1": powershell} {
		if match := pinnedReleaseCoordinate.FindString(script); match != "" {
			t.Errorf("%s pins a release coordinate (%q). Installers must track the "+
				"default branch; a written-down tag goes stale and nothing about it "+
				"looks wrong.", label, match)
		}
	}
}

// uninstallSection returns install.sh from do_uninstall() onward.
func uninstallSection(t *testing.T, posix string) string {
	t.Helper()
	start := strings.Index(posix, "do_uninstall()")
	if start < 0 {
		t.Fatal("install.sh has no do_uninstall(); this guard would scan the whole file")
	}
	return posix[start:]
}

func TestUninstallHonoursTheRequestedRunnerScope(t *testing.T) {
	// `--runner=codex --uninstall` once removed a working Claude Code install,
	// because uninstall looped over every *detected* runner instead of the
	// requested ones. Removing something the operator did not ask to remove is
	// the worst failure mode either script has.
	posix, powershell := installScripts(t)
	section := uninstallSection(t, posix)
	if !strings.Contains(section, `targets="$RUNNERS"`) {
		t.Error("install.sh's uninstall does not iterate the requested runners")
	}
	if strings.Contains(section, "for runner in $(detect_runners); do") {
		t.Error("install.sh's uninstall iterates every detected runner again, which " +
			"is what removed a working Claude Code install on --runner=codex")
	}
	// The checkout and launcher are shared, so a scoped uninstall must not
	// delete them out from under the runners left installed.
	if !strings.Contains(section, `if [ "$scoped" -eq 1 ]`) {
		t.Error("install.sh's uninstall does not distinguish a scoped removal, so it " +
			"deletes shared artifacts the remaining runners still need")
	}
	if !strings.Contains(powershell, "Invoke-Uninstall -Targets $targets -Scoped:$scoped") {
		t.Error("install.ps1 does not pass the requested targets and scope to uninstall")
	}
	if !strings.Contains(powershell, "if ($Scoped)") {
		t.Error("install.ps1's uninstall never checks $Scoped")
	}
}

func TestBothInstallersOfferTheSameSwitches(t *testing.T) {
	// They are separate implementations of one behaviour; nothing but this
	// keeps them aligned.
	posix, powershell := installScripts(t)
	for _, pair := range []struct{ shell, ps string }{
		{"--dry-run", "$DryRun"},
		{"--uninstall", "$Uninstall"},
		{"--with-lifecycle", "$WithLifecycle"},
		{"--runner=", "$Runner"},
	} {
		if !strings.Contains(posix, pair.shell) {
			t.Errorf("install.sh does not offer %s", pair.shell)
		}
		if !strings.Contains(powershell, pair.ps) {
			t.Errorf("install.ps1 has no %s, the counterpart of %s", pair.ps, pair.shell)
		}
	}
}

func TestBothInstallersProtectTheCodexConfigTheyEdit(t *testing.T) {
	posix, powershell := installScripts(t)
	for label, script := range map[string]string{"install.sh": posix, "install.ps1": powershell} {
		// It is a file the operator owns and may have edited by hand.
		if !strings.Contains(script, "cadre-backup") {
			t.Errorf("%s edits the Codex config without backing it up first", label)
		}
		// A fenced block is what makes a re-run replace its own section rather
		// than append a second copy.
		if !strings.Contains(script, ">>> cadre >>>") || !strings.Contains(script, "<<< cadre <<<") {
			t.Errorf("%s does not fence its Codex config block, so a re-run appends "+
				"a duplicate instead of replacing it", label)
		}
		// `codex plugin marketplace add` is a no-op on an already-configured
		// marketplace and does not refresh it, so without an explicit upgrade a
		// re-run keeps serving a stale snapshot -- observed serving pre-monorepo
		// content.
		if !strings.Contains(script, "marketplace upgrade") {
			t.Errorf("%s never upgrades the Codex marketplace snapshot, so a re-run "+
				"keeps serving stale content", label)
		}
	}
}
