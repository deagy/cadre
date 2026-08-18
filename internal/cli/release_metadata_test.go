package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Ported from plugin/tools/test_plugin_version.py and
// plugin/tools/test_changelog_entry.py.

// scratchPackage builds a temporary package tree with all eight manifests, so
// the version tests exercise the real path derivation rather than a substituted
// list of two files.
func scratchPackage(t *testing.T, version string) string {
	t.Helper()
	root := t.TempDir()
	for _, path := range pluginManifests(root) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		body := "{\n  \"name\": \"cadre\",\n  \"version\": \"" + version + "\"\n}\n"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return root
}

func TestEveryManifestIsWrittenTogether(t *testing.T) {
	root := scratchPackage(t, "0.1.0")
	if err := setPluginVersion(root, "0.2.0"); err != nil {
		t.Fatalf("setPluginVersion: %v", err)
	}
	versions, err := readPluginVersions(root)
	if err != nil {
		t.Fatalf("readPluginVersions: %v", err)
	}
	if len(versions) != 8 {
		t.Fatalf("read %d manifests, expected 4 plugins x 2 each", len(versions))
	}
	for name, version := range versions {
		if version != "0.2.0" {
			t.Errorf("%s is at %q, not the version just set", name, version)
		}
	}
}

func TestAFailureOnOneManifestLeavesTheOthersUntouched(t *testing.T) {
	// The whole point of the all-or-nothing write: every manifest is validated
	// before any of them is written to disk.
	root := scratchPackage(t, "0.2.0")
	manifests := pluginManifests(root)
	intact := manifests["claude"]
	before, err := os.ReadFile(intact)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	broken := "{\n  \"name\": \"cadre\",\n  \"ver_sion\": \"0.2.0\"\n}\n"
	if err := os.WriteFile(manifests["codex"], []byte(broken), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	err = setPluginVersion(root, "0.3.0")
	if err == nil {
		t.Fatal("a manifest with no version line was accepted")
	}
	if !strings.Contains(err.Error(), `could not locate a "version" line`) {
		t.Errorf("the refusal does not say what was wrong:\n%v", err)
	}
	after, _ := os.ReadFile(intact)
	if string(after) != string(before) {
		t.Error("a manifest was rewritten even though a sibling failed validation. " +
			"They have to change together or not at all.")
	}
	corrupt, _ := os.ReadFile(manifests["codex"])
	if string(corrupt) != broken {
		t.Error("the unparseable manifest was written to anyway")
	}
}

func TestNonSemverIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	root := scratchPackage(t, "0.1.0")
	before, _ := os.ReadFile(pluginManifests(root)["claude"])
	err := setPluginVersion(root, "0.3")
	if err == nil {
		t.Fatal("a two-part version was accepted as semver")
	}
	if !strings.Contains(err.Error(), "not MAJOR.MINOR.PATCH semver") {
		t.Errorf("the refusal does not name the expected shape:\n%v", err)
	}
	after, _ := os.ReadFile(pluginManifests(root)["claude"])
	if string(after) != string(before) {
		t.Error("a manifest was written before the version was validated")
	}
}

func TestDisagreeingManifestsAreReported(t *testing.T) {
	root := scratchPackage(t, "0.2.0")
	body := "{\n  \"name\": \"cadre\",\n  \"version\": \"0.3.0\"\n}\n"
	if err := os.WriteFile(pluginManifests(root)["codex"], []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	problems, _, err := checkPluginVersions(root)
	if err != nil {
		t.Fatalf("checkPluginVersions: %v", err)
	}
	if len(problems) != 1 {
		t.Fatalf("reported %d problems, expected exactly the disagreement: %v", len(problems), problems)
	}
	if !strings.Contains(problems[0], "disagree on version") {
		t.Errorf("the report does not name the disagreement: %q", problems[0])
	}
}

func TestThisRepositorysOwnManifestsAgreeOnAValidVersion(t *testing.T) {
	root := filepath.Join(filepath.Dir(filepath.Dir(mustGetwd(t))), "plugin")
	problems, versions, err := checkPluginVersions(root)
	if err != nil {
		t.Fatalf("checkPluginVersions: %v", err)
	}
	if len(problems) > 0 {
		t.Fatalf("the committed manifests are not releasable:\n  %s", strings.Join(problems, "\n  "))
	}
	if len(versions) != 8 {
		t.Fatalf("read %d manifests, expected 4 plugins x 2 each", len(versions))
	}
	distinct := map[string]bool{}
	for _, version := range versions {
		distinct[version] = true
	}
	if len(distinct) != 1 {
		t.Fatalf("the manifests carry %d distinct versions: %v", len(distinct), versions)
	}
}

var advertisedRoleCount = regexp.MustCompile(`(\d+) specialist roles`)

func TestTheAdvertisedRoleCountMatchesThePackagedCatalog(t *testing.T) {
	// The core plugin's two manifests advertise a role count in their
	// description. The generator copies that prose faithfully, so a stale
	// number passes every drift check -- only a comparison against the catalog
	// itself catches it.
	//
	// The three lifecycle plugins do not describe the role catalog at all, so
	// they are excluded rather than made to say something untrue of them.
	repoRoot := filepath.Dir(filepath.Dir(mustGetwd(t)))
	raw, err := os.ReadFile(filepath.Join(repoRoot, "provider", "agent-catalog.json"))
	if err != nil {
		t.Fatalf("cannot read the committed agent catalog: %v", err)
	}
	// An object keyed by role id, not an array. Python's len() reads both the
	// same way, so the shape never had to be pinned down there.
	var catalog struct {
		Agents map[string]json.RawMessage `json:"agents"`
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatalf("agent-catalog.json does not parse: %v", err)
	}
	expected := len(catalog.Agents)
	if expected == 0 {
		t.Fatal("the catalog lists no agents; this comparison would be vacuous")
	}
	manifests := pluginManifests(filepath.Join(repoRoot, "plugin"))
	for _, name := range []string{"claude", "codex"} {
		content, err := os.ReadFile(manifests[name])
		if err != nil {
			t.Fatalf("cannot read the %s manifest: %v", name, err)
		}
		matches := advertisedRoleCount.FindAllStringSubmatch(string(content), -1)
		if len(matches) == 0 {
			t.Errorf("the %s manifest no longer advertises a role count, so nothing "+
				"ties its description to the catalog", name)
			continue
		}
		for _, match := range matches {
			advertised, _ := strconv.Atoi(match[1])
			if advertised != expected {
				t.Errorf("the %s manifest advertises %d specialist roles; the catalog "+
					"holds %d. The generator copies this prose faithfully, so a stale "+
					"number passes every other drift check.", name, advertised, expected)
			}
		}
	}
}

const sampleChangelog = `# Changelog

## [0.3.0](https://example.invalid/v0.3.0) - 2026-01-02

Third release.

## [0.2.0](https://example.invalid/v0.2.0) - 2026-01-01

Second release.

## 0.1.0 - 2025-12-31

First release, with no link in the heading.
`

func TestExtractsOnlyTheNamedVersionsBody(t *testing.T) {
	entry, err := extractChangelogEntry("0.3.0", sampleChangelog)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !strings.Contains(entry, "Third release.") {
		t.Errorf("the entry does not carry its own body:\n%s", entry)
	}
	if strings.Contains(entry, "Second release.") {
		t.Errorf("the entry runs into the next version's body:\n%s", entry)
	}
}

func TestTheLastEntryRunsToTheEndOfTheFile(t *testing.T) {
	// 0.2.0 is followed by an unlinked heading, which is not a section start,
	// so its body legitimately continues to the end.
	entry, err := extractChangelogEntry("0.2.0", sampleChangelog)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !strings.Contains(entry, "Second release.") || !strings.Contains(entry, "First release") {
		t.Errorf("the last matched entry does not run to the end of the file:\n%s", entry)
	}
}

func TestAHeadingWithoutAReleaseLinkIsNotMatched(t *testing.T) {
	// The format is enforced, not merely followed. A plain heading is treated
	// as "entry not found" rather than guessed at.
	if _, err := extractChangelogEntry("0.1.0", sampleChangelog); err == nil {
		t.Error("an unlinked heading was accepted as a version section")
	}
}

func TestAMissingVersionFailsLoudlyRatherThanReturningEmpty(t *testing.T) {
	// An empty release body publishes silently; an error fails the release.
	_, err := extractChangelogEntry("9.9.9", sampleChangelog)
	if err == nil {
		t.Fatal("a missing version produced no error")
	}
	if !strings.Contains(err.Error(), "9.9.9") {
		t.Errorf("the error does not name the version asked for:\n%v", err)
	}
}

func TestThisRepositorysChangelogHasAnEntryForItsCurrentVersion(t *testing.T) {
	packageRoot := filepath.Join(filepath.Dir(filepath.Dir(mustGetwd(t))), "plugin")
	_, versions, err := checkPluginVersions(packageRoot)
	if err != nil {
		t.Fatalf("reading the current version: %v", err)
	}
	var current string
	for _, version := range versions {
		current = version
		break
	}
	content, err := os.ReadFile(filepath.Join(packageRoot, "CHANGELOG.md"))
	if err != nil {
		t.Fatalf("cannot read the packaged CHANGELOG.md: %v", err)
	}
	entry, err := extractChangelogEntry(current, string(content))
	if err != nil {
		t.Fatalf("the manifests declare %s but the CHANGELOG has no entry for it. "+
			"A release cut now would publish an empty body: %v", current, err)
	}
	if strings.TrimSpace(entry) == "" {
		t.Errorf("the CHANGELOG entry for %s is empty", current)
	}
}
