package generators

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// plugin/README.md must keep describing this repository's actual four-plugin
// split, not the generator's generic single-plugin template.
//
// generate-plugin's guard against writing into a non-empty --output refuses
// unless the target already holds a .codex-plugin/plugin.json -- and plugin/
// has one, being itself a packaged plugin. So the guard passes trivially and
// does not stop a clobber (deagy/cadre#97).
//
// This is a positive assertion, not a diff against the template: the template
// is a string inside the generator, not a file to compare with. Instead it
// asserts README.md still carries content the generic template would never
// produce, so a clobber fails here rather than waiting to be noticed.
//
// Ported from plugin/tools/test_readme_identity.py.

const pluginReadmeTitle = "# `plugin/` — the packaged distribution\n"

// pluginNames is every plugin manifest this repository ships.
var pluginNames = []string{
	"cadre",
	"cadre-lifecycle-core",
	"cadre-lifecycle-github",
	"cadre-lifecycle-gitlab",
}

// readmeIdentityMarkers is content only this hand-authored README has. The
// template describes a standalone single-plugin repository and would produce
// neither a generated-vs-hand-authored table nor a pointer to the canonical
// install guide, so their absence is a reliable clobber signal.
var readmeIdentityMarkers = []string{
	"## What is generated and what is not",
	"docs/INSTALL.md",
	"It is **not a\nrepository**",
}

func pluginReadme(t *testing.T) string {
	t.Helper()
	path := filepath.Join(repositoryRoot(t), "plugin", "README.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	return string(content)
}

func TestThePluginReadmeKeepsItsOwnTitle(t *testing.T) {
	// The title was "# Cadre Lifecycle" until the monorepo merge, when this
	// stopped being a repository and became a directory inside one.
	if !strings.HasPrefix(pluginReadme(t), pluginReadmeTitle) {
		t.Errorf("plugin/README.md's title has changed -- possible clobber by the "+
			"generator's template, which describes a standalone single-plugin "+
			"repository this directory is not. Expected it to start %q.", pluginReadmeTitle)
	}
}

func TestThePluginReadmeNamesAllFourPlugins(t *testing.T) {
	// As a whole name, not a substring. "cadre" is a prefix of all three
	// lifecycle plugins, so a plain containment check could never fail for it
	// -- a README that dropped the standalone plugin entirely but still named
	// cadre-lifecycle-core would have passed.
	readme := pluginReadme(t)
	for _, name := range pluginNames {
		if !namedAsAWholePlugin(readme, name) {
			t.Errorf("plugin/README.md no longer mentions the %q plugin -- possible "+
				"clobber by the generic single-plugin template", name)
		}
	}
}

// namedAsAWholePlugin reports whether text names exactly this plugin, rather
// than only some longer name that starts with it.
func namedAsAWholePlugin(text, name string) bool {
	for offset := 0; ; {
		index := strings.Index(text[offset:], name)
		if index < 0 {
			return false
		}
		end := offset + index + len(name)
		if end >= len(text) || !isPluginNameByte(text[end]) {
			return true
		}
		offset += index + 1
	}
}

func isPluginNameByte(character byte) bool {
	return character == '-' || character == '_' ||
		(character >= 'a' && character <= 'z') ||
		(character >= 'A' && character <= 'Z') ||
		(character >= '0' && character <= '9')
}

func TestThePluginReadmeCarriesItsIdentityMarkers(t *testing.T) {
	readme := pluginReadme(t)
	for _, marker := range readmeIdentityMarkers {
		if !strings.Contains(readme, marker) {
			t.Errorf("plugin/README.md is missing %q -- possible clobber by the "+
				"generator's template", marker)
		}
	}
}

func TestThePluginReadmeDoesNotReintroduceInstallInstructions(t *testing.T) {
	// Install steps belong in docs/INSTALL.md only. Three documents quoting
	// three different stale version tags is what one canonical page exists to
	// prevent; a second copy here is how that starts again.
	readme := pluginReadme(t)
	for _, command := range []string{"/plugin marketplace add", "codex plugin marketplace add"} {
		if strings.Contains(readme, command) {
			t.Errorf("plugin/README.md carries %q. Install steps live in docs/INSTALL.md, "+
				"so that there is one page to keep current rather than three.", command)
		}
	}
}
