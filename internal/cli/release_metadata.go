package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Read, check, or set the release version, and extract a version's changelog
// entry.
//
// Ported from plugin/tools/plugin_version.py and plugin/tools/changelog_entry.py,
// both of which .github/workflows/release.yml called directly.

// pluginManifests are the eight manifests that must always agree on a version.
//
// This repository packages four independently-installable plugins -- the core
// role-selection plugin at plugin/, plus three optional lifecycle-governance
// plugins -- each declaring its version in a Claude Code manifest and a Codex
// one. They share one version number: a release bumps every plugin together,
// even if only one actually changed. Kept flat rather than grouped per plugin,
// because nothing below needs the grouping.
//
// Nothing regenerates this field. It is set only through this command, so a
// release is always a deliberate, reviewed action, and all eight manifests are
// hand-authored package assets outside the generated tree -- so setting a
// version here never conflicts with regeneration.
func pluginManifests(packageRoot string) map[string]string {
	manifests := map[string]string{
		"claude": filepath.Join(packageRoot, ".claude-plugin", "plugin.json"),
		"codex":  filepath.Join(packageRoot, ".codex-plugin", "plugin.json"),
	}
	for _, plugin := range []string{"lifecycle", "lifecycle-github", "lifecycle-gitlab"} {
		manifests[plugin+"-claude"] = filepath.Join(packageRoot, "plugins", plugin, ".claude-plugin", "plugin.json")
		manifests[plugin+"-codex"] = filepath.Join(packageRoot, "plugins", plugin, ".codex-plugin", "plugin.json")
	}
	return manifests
}

var semverPattern = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$`)

// versionLine matches the first line naming a top-level-shaped "version" key.
//
// Line-based rather than JSON-structure-aware, so the rewrite preserves the
// file's exact formatting. setVersion always re-parses the result and checks it
// against the intended value before accepting it, so a manifest shape this
// cannot handle fails loudly instead of writing wrong content -- do not remove
// that check as redundant.
var versionLine = regexp.MustCompile(`(?m)^(\s*"version"\s*:\s*")[^"]*(",?\s*)$`)

func readPluginVersions(packageRoot string) (map[string]string, error) {
	versions := map[string]string{}
	for name, path := range pluginManifests(packageRoot) {
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("missing manifest %s", path)
		}
		var manifest map[string]any
		if err := json.Unmarshal(content, &manifest); err != nil {
			return nil, fmt.Errorf("%s does not parse: %w", path, err)
		}
		version, present := manifest["version"].(string)
		if !present {
			return nil, fmt.Errorf("%s has no \"version\" field", path)
		}
		versions[name] = version
	}
	return versions, nil
}

// checkPluginVersions returns every reason the manifests are not releasable.
func checkPluginVersions(packageRoot string) ([]string, map[string]string, error) {
	versions, err := readPluginVersions(packageRoot)
	if err != nil {
		return nil, nil, err
	}
	manifests := pluginManifests(packageRoot)
	var problems []string
	names := make([]string, 0, len(versions))
	for name := range versions {
		names = append(names, name)
	}
	sort.Strings(names)
	distinct := map[string]bool{}
	var rendered []string
	for _, name := range names {
		version := versions[name]
		distinct[version] = true
		rendered = append(rendered, name+"="+version)
		if !semverPattern.MatchString(version) {
			problems = append(problems,
				fmt.Sprintf("%s: %q is not MAJOR.MINOR.PATCH semver", manifests[name], version))
		}
	}
	if len(distinct) > 1 {
		problems = append(problems,
			"plugin manifests disagree on version: "+strings.Join(rendered, ", "))
	}
	return problems, versions, nil
}

func setPluginVersion(packageRoot, version string) error {
	if !semverPattern.MatchString(version) {
		return fmt.Errorf("%q is not MAJOR.MINOR.PATCH semver", version)
	}
	// Every manifest's new content is built and validated before any of them
	// is written, so a problem with one can never leave a different one already
	// rewritten on disk. They change together or not at all.
	updates := map[string]string{}
	for _, path := range pluginManifests(packageRoot) {
		original, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		matches := versionLine.FindAllStringIndex(string(original), -1)
		if len(matches) == 0 {
			return fmt.Errorf("could not locate a \"version\" line in %s", path)
		}
		updated := string(original[:matches[0][0]]) +
			versionLine.ReplaceAllString(string(original[matches[0][0]:matches[0][1]]), "${1}"+version+"${2}") +
			string(original[matches[0][1]:])
		var reparsed map[string]any
		if err := json.Unmarshal([]byte(updated), &reparsed); err != nil {
			return fmt.Errorf("substitution produced invalid JSON in %s: %w", path, err)
		}
		if reparsed["version"] != version {
			return fmt.Errorf("substitution produced unexpected JSON in %s", path)
		}
		updates[path] = updated
	}
	for path, updated := range updates {
		if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
			return err
		}
	}
	return nil
}

const usagePluginVersion = `Read, check, or set the release version shared by all eight plugin manifests.

  cadre plugin-version              print the current, verified version
  cadre plugin-version --check      exit non-zero if unset, mismatched or invalid
  cadre plugin-version --set 0.3.0  write a version into all eight manifests

This creates no git tag and pushes nothing.`

// PluginVersionCmd implements `cadre plugin-version`.
func PluginVersionCmd(args []string) int {
	fs := flag.NewFlagSet("cadre plugin-version", flag.ContinueOnError)
	setUsage(fs, "plugin-version", usagePluginVersion)
	// Bare invocation and --check are deliberately the same read-and-verify:
	// --check exists as a self-documenting flag for CI.
	check := fs.Bool("check", false, "verify the manifests agree on a valid semver version")
	set := fs.String("set", "", "write VERSION (MAJOR.MINOR.PATCH) into all eight manifests")
	root := fs.String("package-root", "plugin", "directory holding the packaged plugin")
	if err := fs.Parse(args); err != nil {
		return parseExitCode(err)
	}
	_ = check
	if *set != "" {
		if err := setPluginVersion(*root, *set); err != nil {
			fmt.Fprintln(os.Stderr, "plugin-version: "+err.Error())
			return 1
		}
		fmt.Println("plugin-version: set to " + *set)
		return 0
	}
	problems, versions, err := checkPluginVersions(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "plugin-version: "+err.Error())
		return 1
	}
	if len(problems) > 0 {
		for _, problem := range problems {
			fmt.Fprintln(os.Stderr, "plugin-version: "+problem)
		}
		return 1
	}
	names := make([]string, 0, len(versions))
	for name := range versions {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Println(versions[names[0]])
	return 0
}

// changelogHeading matches a version section's heading.
//
// The format is enforced here, not merely followed by convention: a plain
// heading with no release link is not matched, so a malformed entry fails the
// release rather than publishing an empty body.
var changelogHeading = regexp.MustCompile(
	`(?m)^## \[(\d+\.\d+\.\d+)\]\([^)]+\) - \d{4}-\d{2}-\d{2}\s*$`)

// extractChangelogEntry returns one version's release notes.
func extractChangelogEntry(version, changelog string) (string, error) {
	headings := changelogHeading.FindAllStringSubmatchIndex(changelog, -1)
	for index, location := range headings {
		if changelog[location[2]:location[3]] != version {
			continue
		}
		end := len(changelog)
		if index+1 < len(headings) {
			end = headings[index+1][0]
		}
		return strings.Trim(changelog[location[1]:end], "\n") + "\n", nil
	}
	return "", fmt.Errorf("no CHANGELOG.md entry found for version %s", version)
}

const usageChangelogEntry = `Print one version's release notes from the packaged CHANGELOG.md.

  cadre changelog-entry 0.2.4

A version whose section is missing or malformed is an error rather than an
empty body, so a bad entry fails the release instead of publishing silence.`

// ChangelogEntryCmd implements `cadre changelog-entry`.
func ChangelogEntryCmd(args []string) int {
	fs := flag.NewFlagSet("cadre changelog-entry", flag.ContinueOnError)
	setUsage(fs, "changelog-entry", usageChangelogEntry)
	root := fs.String("package-root", "plugin", "directory holding the packaged CHANGELOG.md")
	if err := fs.Parse(args); err != nil {
		return parseExitCode(err)
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: cadre changelog-entry MAJOR.MINOR.PATCH")
		return 2
	}
	content, err := os.ReadFile(filepath.Join(*root, "CHANGELOG.md"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "changelog-entry: "+err.Error())
		return 1
	}
	entry, err := extractChangelogEntry(fs.Arg(0), string(content))
	if err != nil {
		fmt.Fprintln(os.Stderr, "changelog-entry: "+err.Error())
		return 1
	}
	fmt.Print(entry)
	return 0
}
