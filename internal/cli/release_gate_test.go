package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deagy/cadre/cli/internal/version"
)

// Ported from the Python heredoc in release.yml's `changed` job, which had
// been silently gating the CLI release off entirely.

func TestEveryWatchedReleasePathExists(t *testing.T) {
	// The guard the original lacked, and the whole reason it broke.
	//
	// The Python listed cadre_cli/_version.py. That marker moved to VERSION
	// when the last Python left the distribution. The file was then absent at
	// both ends of every comparison, both sides read as nil, nil equalled nil,
	// and the gate reported "cli: version unchanged in this push" forever.
	// Nothing failed: a release that does not happen produces no error, only a
	// missing artifact nobody looks for until they need it.
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	for _, component := range releaseComponents(root) {
		if len(component.paths) == 0 {
			t.Errorf("component %q watches no paths, so it can never be released",
				component.name)
			continue
		}
		found := 0
		for _, path := range component.paths {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err == nil {
				found++
				continue
			}
			if !component.anyOf {
				t.Errorf("%s watches %s, which does not exist. A path that is absent "+
					"at both ends of a comparison reads as unchanged, so this "+
					"component would never be released.", component.name, path)
			}
		}
		if found == 0 {
			t.Errorf("none of %s's watched paths exist (%s), so its version can never "+
				"be seen to change", component.name, strings.Join(component.paths, ", "))
		}
	}
}

func TestTheCliComponentWatchesTheMarkerTheCliItselfReads(t *testing.T) {
	// Stated separately because it is the coupling that broke. Two lists of
	// marker names, maintained apart, is exactly how the gate ended up looking
	// for a file the CLI had stopped using.
	for _, component := range releaseComponents(filepath.Dir(filepath.Dir(mustGetwd(t)))) {
		if component.name != "cli" {
			continue
		}
		if strings.Join(component.paths, "\x00") != strings.Join(version.VersionMarkerNames, "\x00") {
			t.Errorf("the cli component watches %v, but `cadre --version` reads %v",
				component.paths, version.VersionMarkerNames)
		}
		return
	}
	t.Fatal("there is no cli component to release")
}

// scratchRepo builds a git repository with one commit, and returns its path
// plus that commit's sha.
func scratchRepo(t *testing.T, files map[string]string) (root, first string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	root = t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = root
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
		}
		return strings.TrimSpace(string(output))
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.invalid")
	run("config", "user.name", "Test")
	writeScratchFiles(t, root, files)
	run("add", "-A")
	run("commit", "-q", "-m", "first")
	return root, run("rev-parse", "HEAD")
}

func writeScratchFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func commitScratch(t *testing.T, root string, files map[string]string) {
	t.Helper()
	writeScratchFiles(t, root, files)
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "next"}} {
		command := exec.Command("git", args...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
}

// manifestBody is a plugin manifest carrying a version.
func manifestBody(version string) string {
	return "{\n  \"name\": \"cadre\",\n  \"version\": \"" + version + "\"\n}\n"
}

// eightManifests returns every plugin manifest at one version.
func eightManifests(t *testing.T, version string) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, absolute := range pluginManifests("plugin") {
		files[filepath.ToSlash(absolute)] = manifestBody(version)
	}
	if len(files) != 8 {
		t.Fatalf("expected 8 manifests, built %d", len(files))
	}
	return files
}

func detect(t *testing.T, root, before string) map[string]bool {
	t.Helper()
	var diagnostics strings.Builder
	result := changedComponents(root, "push", before, &diagnostics)
	t.Log(strings.TrimSpace(diagnostics.String()))
	return result
}

func TestABumpedCliVersionIsDetected(t *testing.T) {
	// The case the Python could not see at all.
	files := eightManifests(t, "1.0.0")
	files["VERSION"] = "0.5.0\n"
	root, first := scratchRepo(t, files)

	commitScratch(t, root, map[string]string{"VERSION": "0.6.0\n"})

	changed := detect(t, root, first)
	if !changed["cli"] {
		t.Error("a bumped VERSION was not detected, so the CLI would not be released")
	}
	if changed["plugin"] {
		t.Error("the plugin was reported changed when only the CLI version moved")
	}
}

func TestAnUnchangedVersionIsNotReleased(t *testing.T) {
	files := eightManifests(t, "1.0.0")
	files["VERSION"] = "0.5.0\n"
	root, first := scratchRepo(t, files)

	commitScratch(t, root, map[string]string{"README.md": "an unrelated edit\n"})

	changed := detect(t, root, first)
	for name, moved := range changed {
		if moved {
			t.Errorf("%s was reported changed by an edit that touched no version", name)
		}
	}
}

func TestAMarkerThatMovesIsNotReadAsUnchanged(t *testing.T) {
	// The regression, stated directly. The CLI version lived in
	// cadre_cli/_version.py and moved to VERSION. Under the old comparison the
	// new marker was invisible and the old one was absent at both ends, so the
	// component read as unchanged and stopped being released.
	files := eightManifests(t, "1.0.0")
	files["cadre_cli/_version.py"] = "VERSION = \"0.5.0\"\n"
	root, first := scratchRepo(t, files)

	if err := os.Remove(filepath.Join(root, "cadre_cli", "_version.py")); err != nil {
		t.Fatalf("removing the old marker: %v", err)
	}
	commitScratch(t, root, map[string]string{"VERSION": "0.6.0\n"})

	if !detect(t, root, first)["cli"] {
		t.Error("a CLI version that moved to a different marker file read as unchanged")
	}
}

func TestABumpInOnlySomeManifestsStillCountsAsChanged(t *testing.T) {
	// Every manifest is compared, not just the first. A partial bump has to
	// reach the release job so `cadre plugin-version --check` can fail on the
	// disagreement -- rather than the release being skipped with a log line.
	files := eightManifests(t, "1.0.0")
	files["VERSION"] = "0.5.0\n"
	root, first := scratchRepo(t, files)

	commitScratch(t, root, map[string]string{
		"plugin/plugins/lifecycle-gitlab/.codex-plugin/plugin.json": manifestBody("1.1.0"),
	})

	if !detect(t, root, first)["plugin"] {
		t.Error("a version bumped in only one of the eight manifests read as unchanged, " +
			"so the disagreement would never reach the check that reports it")
	}
}

func TestAnUnparseableManifestIsNotReadAsUnchanged(t *testing.T) {
	files := eightManifests(t, "1.0.0")
	files["VERSION"] = "0.5.0\n"
	root, first := scratchRepo(t, files)

	commitScratch(t, root, map[string]string{
		"plugin/.claude-plugin/plugin.json": "{ this is not json\n",
	})

	if !detect(t, root, first)["plugin"] {
		t.Error("an unparseable manifest read as unchanged. It has to reach the job " +
			"so the dedicated check can report it, rather than silently skipping " +
			"the release.")
	}
}

func TestWithNoComparableBeforeEverythingIsReleasable(t *testing.T) {
	// workflow_dispatch carries no before-sha, and a force-push or a branch's
	// first push reports all zeros. Failing closed there would mean a manual
	// release run publishes nothing.
	files := eightManifests(t, "1.0.0")
	files["VERSION"] = "0.5.0\n"
	root, _ := scratchRepo(t, files)

	for _, testCase := range []struct {
		name   string
		event  string
		before string
	}{
		{"a manual run carries no before-sha", "workflow_dispatch", ""},
		{"a first push reports all zeros", "push", strings.Repeat("0", 40)},
		{"the before-sha is not in this checkout", "push", strings.Repeat("a", 40)},
		{"an empty before-sha on a push", "push", ""},
	} {
		var diagnostics strings.Builder
		changed := changedComponents(root, testCase.event, testCase.before, &diagnostics)
		for name, moved := range changed {
			if !moved {
				t.Errorf("%s: %s was reported unchanged, so a release run would "+
					"publish nothing", testCase.name, name)
			}
		}
	}
}

func TestAPresentButUnreadableMarkerIsDistinctFromOneWithNoVersion(t *testing.T) {
	// Three readings have to stay distinct: absent, present-with-no-version,
	// and present-but-unparseable. Collapsing the last two lets a manifest that
	// never had a version field become invalid JSON without the gate noticing,
	// because both read as the empty string -- and a broken manifest that
	// reads as "unchanged" is a release skipped in silence.
	root, _ := scratchRepo(t, map[string]string{
		"broken.json":      "{ this is not json\n",
		"versionless.json": "{\n  \"name\": \"cadre\"\n}\n",
		"good.json":        manifestBody("1.2.3"),
		"absent-marker":    "placeholder\n",
	})

	broken := markerVersionAt(root, "HEAD", "broken.json")
	versionless := markerVersionAt(root, "HEAD", "versionless.json")
	good := markerVersionAt(root, "HEAD", "good.json")
	missing := markerVersionAt(root, "HEAD", "not-committed.json")

	if missing != nil {
		t.Errorf("an absent file read as %q rather than absent", describeMarker(missing))
	}
	if good == nil || *good != "1.2.3" {
		t.Errorf("a valid manifest read as %q", describeMarker(good))
	}
	if broken == nil || *broken != unreadableMarker {
		t.Errorf("unparseable JSON read as %q, not the unreadable sentinel",
			describeMarker(broken))
	}
	if versionless == nil || *versionless != "" {
		t.Errorf("a manifest with no version field read as %q, not empty",
			describeMarker(versionless))
	}
	if describeMarker(broken) == describeMarker(versionless) {
		t.Error("unparseable JSON and a manifest with no version field read the " +
			"same, so one becoming the other would not be seen as a change")
	}
}
