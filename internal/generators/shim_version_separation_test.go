package generators

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The shim carries two version numbers, and they are not the same number.
//
//   - plugin_version, read at runtime from .claude-plugin/plugin.json, is what
//     `--version` prints.
//   - CADRE_CLI_VERSION, embedded at generation time, is what names the release
//     the binary is downloaded from.
//
// Confusing them is not hypothetical: the shim once used plugin_version for
// both, and so asked for a `plugin-v<version>` release that holds no binaries.
// Every download 404s while `--version` keeps printing something plausible.
//
// Ported from plugin/tools/test_version_separation.py.

func generatedShim(t *testing.T) string {
	t.Helper()
	path := filepath.Join(repositoryRoot(t), "plugin", "bin", "cadre")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read the generated shim at %s: %v", path, err)
	}
	return string(content)
}

func TestTheShimKeepsTheCliVersionSeparateFromThePluginVersion(t *testing.T) {
	shim := generatedShim(t)
	for _, required := range []string{"plugin_version=", "CADRE_CLI_VERSION="} {
		if !strings.Contains(shim, required) {
			t.Errorf("the shim does not define %s; the two version lines have "+
				"collapsed into one", required)
		}
	}
	// Assembled rather than written out, so this file does not contain the
	// literal string it forbids -- the scan is over the shim, but a reader
	// grepping the tree should not find a false example here either.
	wrongTag := "plugin-v" + "$VERSION"
	rightTag := "cli-v" + "$VERSION"
	if !strings.Contains(shim, rightTag) {
		t.Errorf("the shim does not download from a %s tag", rightTag)
	}
	if strings.Contains(shim, wrongTag) {
		t.Errorf("the shim downloads from a %s tag. That release namespace holds "+
			"the packaged plugin, not the compiled binaries, so every fetch 404s "+
			"while --version keeps printing a plausible number.", wrongTag)
	}
}

var cliVersionAssignment = regexp.MustCompile(`CADRE_CLI_VERSION="([^"]+)"`)

func TestTheShimPinsAConcreteCliVersion(t *testing.T) {
	// Embedded at generation time, so the value has to be a real version
	// rather than an unexpanded variable or an empty string.
	match := cliVersionAssignment.FindStringSubmatch(generatedShim(t))
	if match == nil {
		t.Fatal("CADRE_CLI_VERSION is not assigned a quoted value")
	}
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(match[1]) {
		t.Errorf("CADRE_CLI_VERSION is %q, which is not an x.y.z version", match[1])
	}
}

func TestTheShimExplainsThatTheCliVersionIsPinnedAtGenerationTime(t *testing.T) {
	// A comment, and deliberately so. The next person to read this shim sees a
	// hardcoded version where every other value is resolved at runtime, and
	// the obvious "fix" is to make it a runtime lookup -- which reintroduces
	// the bug above. The comment is what tells them not to.
	shim := generatedShim(t)
	for _, phrase := range []string{"generation time", "Regenerating"} {
		if !strings.Contains(shim, phrase) {
			t.Errorf("the shim no longer explains %q. Without it the pinned "+
				"CADRE_CLI_VERSION reads as an oversight rather than the point.", phrase)
		}
	}
}

func TestTheVersionFlagStillReadsThePluginManifestAtRuntime(t *testing.T) {
	// --version must stay free of both Python and the network: it reads the
	// manifest sitting next to it with sed and prints. If it ever needed the
	// downloaded binary, `cadre --version` would fail on a cold cache with no
	// connection, which is exactly when someone runs it.
	shim := generatedShim(t)
	start := strings.Index(shim, `if [ "$command_name" = "--version"`)
	if start < 0 {
		t.Fatal("the shim has no --version fast path")
	}
	end := strings.Index(shim[start:], "fi")
	if end < 0 {
		t.Fatal("the --version branch is never closed; the slice below would be the rest of the file")
	}
	section := shim[start : start+end+2]
	for _, required := range []string{"sed -n", ".claude-plugin/plugin.json"} {
		if !strings.Contains(section, required) {
			t.Errorf("the --version fast path does not use %q:\n%s", required, section)
		}
	}
}
