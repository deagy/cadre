package generators

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Structural checks on what the packaged plugins actually declare. Both
// classes of bug below had already shipped once.
//
// Ported from plugin/tools/test_manifest_health.py.

var lifecyclePlugins = []string{"lifecycle", "lifecycle-github", "lifecycle-gitlab"}

var manifestKinds = []string{".claude-plugin", ".codex-plugin"}

func packageRootDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(repositoryRoot(t), "plugin")
}

func readManifest(t *testing.T, directory, kind string) map[string]any {
	t.Helper()
	path := filepath.Join(directory, kind, "plugin.json")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(content, &manifest); err != nil {
		t.Fatalf("%s does not parse: %v", path, err)
	}
	return manifest
}

var frontmatterBlock = regexp.MustCompile(`(?s)\A---\n(.*?)\n---\n`)

func TestEveryPackagedSkillHasParseableFrontmatter(t *testing.T) {
	// Four hand-authored skills once had a description containing ": ", which
	// ends a plain YAML scalar. The files looked completely normal and the
	// skills loaded at runtime with every frontmatter field silently dropped
	// -- a skill with no name or description is undiscoverable.
	root := packageRootDir(t)
	seen := 0
	var broken []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Name() != "SKILL.md" {
			return nil
		}
		seen++
		relative, _ := filepath.Rel(repositoryRoot(t), path)
		relative = filepath.ToSlash(relative)
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			broken = append(broken, relative+": "+readErr.Error())
			return nil
		}
		match := frontmatterBlock.FindSubmatch(content)
		if match == nil {
			broken = append(broken, relative+": no frontmatter block")
			return nil
		}
		var parsed map[string]any
		if yamlErr := yaml.Unmarshal(match[1], &parsed); yamlErr != nil {
			broken = append(broken, relative+": "+strings.SplitN(yamlErr.Error(), "\n", 2)[0])
			return nil
		}
		for _, field := range []string{"name", "description"} {
			value, present := parsed[field]
			text, isString := value.(string)
			if !present || !isString || strings.TrimSpace(text) == "" {
				broken = append(broken, relative+": empty "+field)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if seen == 0 {
		t.Fatal("no SKILL.md found under plugin/; this guard checked nothing")
	}
	if len(broken) > 0 {
		t.Errorf("%d packaged skill(s) have unusable frontmatter:\n  %s\n\n"+
			"Frontmatter must parse as YAML and carry a name and a description. "+
			"A plain scalar containing \": \" ends the value early -- use a folded "+
			"block scalar (>-) for prose.", len(broken), strings.Join(broken, "\n  "))
	}
	t.Logf("checked frontmatter on %d packaged skills", seen)
}

func TestLifecyclePluginsDeclareTheirDependencyInArrayForm(t *testing.T) {
	// Every lifecycle skill shells out to `bin/cadre sdlc`, which exists only
	// in the cadre plugin. That requirement used to be stated in prose in a
	// description field and enforced by nothing.
	//
	// The shape matters as much as the content: dependencies is an array, and
	// the object form {"cadre": ">=x"} is rejected outright by Claude Code, so
	// a plugin declaring it that way installs with no enforcement at all.
	for _, name := range lifecyclePlugins {
		for _, kind := range manifestKinds {
			manifest := readManifest(t, filepath.Join(packageRootDir(t), "plugins", name), kind)
			declared, present := manifest["dependencies"]
			if !present {
				t.Errorf("%s/%s declares no dependencies, so nothing enforces that the "+
					"cadre plugin (which owns `bin/cadre sdlc`) is installed", name, kind)
				continue
			}
			entries, isArray := declared.([]any)
			if !isArray {
				t.Errorf("%s/%s declares dependencies as %T, not an array. Claude Code "+
					"rejects the object form outright, which installs with no dependency "+
					"enforcement at all.", name, kind, declared)
				continue
			}
			found := false
			for _, entry := range entries {
				switch value := entry.(type) {
				case string:
					found = found || value == "cadre"
				case map[string]any:
					text, _ := value["name"].(string)
					found = found || text == "cadre"
				}
			}
			if !found {
				t.Errorf("%s/%s does not depend on cadre", name, kind)
			}
		}
	}
}

func TestTheCadrePluginItselfDeclaresNoDependencies(t *testing.T) {
	// It must stay standalone-installable: being reachable from any project
	// without pulling in lifecycle governance is its whole pitch.
	for _, kind := range manifestKinds {
		if _, present := readManifest(t, packageRootDir(t), kind)["dependencies"]; present {
			t.Errorf("plugin/%s/plugin.json declares dependencies. The cadre plugin has "+
				"to stay installable on its own.", kind)
		}
	}
}

func TestNoManifestDeclaresTheStandardHooksPath(t *testing.T) {
	// hooks/hooks.json at the standard path loads automatically. Naming it in
	// the manifest as well is not redundant-but-harmless: Claude Code reports
	// "Duplicate hooks file detected" and the hook does not load at all. This
	// shipped once -- a SessionStart hook that passed every static check and
	// silently never ran. The manifest field is for additional hook files.
	root := packageRootDir(t)
	directories := []string{root}
	for _, name := range lifecyclePlugins {
		directories = append(directories, filepath.Join(root, "plugins", name))
	}
	for _, directory := range directories {
		for _, kind := range manifestKinds {
			declared, _ := readManifest(t, directory, kind)["hooks"].(string)
			if declared == "./hooks/hooks.json" || declared == "hooks/hooks.json" {
				relative, _ := filepath.Rel(repositoryRoot(t), filepath.Join(directory, kind))
				t.Errorf("%s/plugin.json declares hooks: %q. Remove the field -- "+
					"hooks/hooks.json is loaded automatically, and declaring it makes the "+
					"load fail as a duplicate.", filepath.ToSlash(relative), declared)
			}
		}
	}
}

func TestLifecyclePluginsStillShipAHooksFile(t *testing.T) {
	// The field is removed; the file must remain.
	for _, name := range lifecyclePlugins {
		path := filepath.Join(packageRootDir(t), "plugins", name, "hooks", "hooks.json")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s ships no hooks/hooks.json: %v", name, err)
		}
	}
}

func TestLifecyclePluginsDeclareTheDocumentedUserConfigFields(t *testing.T) {
	// type/title/description are all required by Claude Code; a missing one
	// fails `claude plugin validate`.
	for _, name := range lifecyclePlugins {
		for _, kind := range manifestKinds {
			options, _ := readManifest(t, filepath.Join(packageRootDir(t), "plugins", name),
				kind)["userConfig"].(map[string]any)
			if options == nil {
				t.Errorf("%s/%s declares no userConfig", name, kind)
				continue
			}
			for _, required := range []string{"kernelInstall", "profile"} {
				if _, present := options[required]; !present {
					t.Errorf("%s/%s userConfig has no %q option", name, kind, required)
				}
			}
			for key, raw := range options {
				option, _ := raw.(map[string]any)
				if option == nil {
					t.Errorf("%s/%s userConfig %q is not an object", name, kind, key)
					continue
				}
				for _, field := range []string{"type", "title", "description"} {
					if _, present := option[field]; !present {
						t.Errorf("%s/%s userConfig %q is missing %q, which fails "+
							"`claude plugin validate`", name, kind, key, field)
					}
				}
			}
		}
	}
}

func TestKernelInstallDefaultsToAuto(t *testing.T) {
	for _, name := range lifecyclePlugins {
		options, _ := readManifest(t, filepath.Join(packageRootDir(t), "plugins", name),
			".claude-plugin")["userConfig"].(map[string]any)
		option, _ := options["kernelInstall"].(map[string]any)
		if option == nil {
			t.Errorf("%s declares no kernelInstall option", name)
			continue
		}
		if value, _ := option["default"].(string); value != "auto" {
			t.Errorf("%s's kernelInstall defaults to %q, not \"auto\"", name, value)
		}
	}
}
