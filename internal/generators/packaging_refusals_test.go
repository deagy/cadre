package generators

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// What the plugin generator refuses to package.
//
// The committed distribution is checked byte-for-byte against a fresh
// generation on every pull request, which is a strong guarantee and a
// self-referential one: it says a *good* checkout produces the committed
// output. It says nothing about what a bad one produces, because the
// comparison never runs on a bad one.
//
// These are the refusals. Each is implemented and none was tested on the Go
// side -- the tests that covered them ran against the Python generator, which
// is being retired now that `cadre generate-plugin` dispatches to this code
// and reproduces the committed distribution exactly.

func TestARoleDefinitionGitDoesNotTrackIsRefused(t *testing.T) {
	// The regeneration gotcha this repository's own CLAUDE.md leads with:
	// `git add` new files *before* regenerating, because packaging copies
	// tracked content. An untracked AGENT.md would otherwise pass silently and
	// still get a wrapper and an agent-catalog.json entry -- producing a
	// package that references a suite file nobody copied.
	//
	// Checked against the live catalog rather than a fixture, because that is
	// where the property has to hold: every definition roster/catalog.yaml
	// names must be tracked right now.
	root := repositoryRoot(t)
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		t.Skipf("not a git checkout: %v", err)
	}
	tracked, err := gitTrackedFiles(root, "roster")
	if err != nil {
		t.Fatalf("gitTrackedFiles: %v", err)
	}
	if len(tracked) < 100 {
		t.Fatalf("git tracks only %d files under roster/; the walk is broken, "+
			"not the catalog", len(tracked))
	}
	known := make(map[string]bool, len(tracked))
	for _, path := range tracked {
		known[path] = true
	}

	definitions := catalogDefinitions(t, filepath.Join(root, "roster", "catalog.yaml"))
	if len(definitions) == 0 {
		t.Fatal("the catalog declares no roles")
	}

	var untracked []string
	for agentID, definition := range definitions {
		if !known["roster/"+definition] {
			untracked = append(untracked, agentID+" -> "+definition)
		}
	}
	sort.Strings(untracked)
	if len(untracked) > 0 {
		t.Errorf("roster/catalog.yaml names %d definition(s) git does not track: %v\n"+
			"Packaging copies tracked content, so these would get a wrapper and a "+
			"catalog entry with no suite file behind them. `git add` them.",
			len(untracked), untracked)
	}
}

func TestGitTrackedFilesDistinguishesTrackedFromPresent(t *testing.T) {
	// The distinction the refusal above rests on. A file existing on disk and
	// a file git tracks are different questions, and a helper that answered
	// the first would make that refusal unreachable -- every new role would
	// look fine until the package shipped without it.
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.email", "t@example.invalid"},
		{"config", "user.name", "T"}, {"config", "commit.gpgsign", "false"},
	} {
		command := exec.Command("git", args...)
		command.Dir = root
		command.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if output, err := command.CombinedOutput(); err != nil {
			t.Skipf("git %v failed here: %v\n%s", args, err, output)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "roster"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"committed.md", "untracked.md"} {
		if err := os.WriteFile(filepath.Join(root, "roster", name),
			[]byte("# "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"add", "roster/committed.md"}, {"commit", "-q", "-m", "one"},
	} {
		command := exec.Command("git", args...)
		command.Dir = root
		command.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if output, err := command.CombinedOutput(); err != nil {
			t.Skipf("git %v failed here: %v\n%s", args, err, output)
		}
	}

	tracked, err := gitTrackedFiles(root, "roster")
	if err != nil {
		t.Fatalf("gitTrackedFiles: %v", err)
	}
	found := map[string]bool{}
	for _, path := range tracked {
		found[path] = true
	}
	if !found["roster/committed.md"] {
		t.Error("a committed file was not reported as tracked")
	}
	if found["roster/untracked.md"] {
		t.Error("a file that exists but was never added was reported as tracked; " +
			"the untracked-definition refusal cannot fire")
	}

	// A tracked path whose file has since been deleted is not reported either
	// -- the helper pairs `git ls-files` with a stat, and packaging a path
	// with nothing behind it is the failure it is avoiding.
	if err := os.Remove(filepath.Join(root, "roster", "committed.md")); err != nil {
		t.Fatal(err)
	}
	after, err := gitTrackedFiles(root, "roster")
	if err != nil {
		t.Fatalf("gitTrackedFiles: %v", err)
	}
	for _, path := range after {
		if path == "roster/committed.md" {
			t.Error("a tracked path with no file behind it was still reported")
		}
	}
}

func TestAnAgentCatalogWithAnUnexpectedDefinitionPrefixIsRefused(t *testing.T) {
	// rewriteAgentCatalog re-points every definition at the package's own
	// suite/roster/ copy by swapping a known prefix. A definition that does
	// not carry that prefix cannot be re-pointed, and rewriting it anyway
	// would emit a path into the package that resolves to nothing.
	//
	// The refusal names the command that regenerates the register, because
	// the usual cause is a provider/ that was not regenerated.
	directory := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	good := write("good.json", `{"agents": {"cloud-architect": {`+
		`"definition": "`+providerDefinitionPrefix+`architecture/cloud-architect/AGENT.md"}}}`)
	rewritten, err := rewriteAgentCatalog(good)
	if err != nil {
		t.Fatalf("a well-formed catalog was refused: %v", err)
	}
	if !strings.Contains(rewritten, packageDefinitionPrefix) {
		t.Errorf("the definition was not re-pointed at the package copy:\n%s", rewritten)
	}
	if strings.Contains(rewritten, `"`+providerDefinitionPrefix) {
		t.Errorf("the register-relative prefix survived into the package:\n%s", rewritten)
	}

	for _, probe := range []struct{ name, definition string }{
		{"a different top-level directory", "elsewhere/review/code-reviewer/AGENT.md"},
		{"no directory at all", "AGENT.md"},
		{"an absolute path", "/etc/passwd"},
		{"an empty definition", ""},
	} {
		t.Run(probe.name, func(t *testing.T) {
			path := write("bad.json", `{"agents": {"a": {"definition": "`+probe.definition+`"}}}`)
			_, err := rewriteAgentCatalog(path)
			if err == nil {
				t.Fatalf("a definition of %q was accepted", probe.definition)
			}
			if !strings.Contains(err.Error(), "generate-role-metadata") {
				t.Errorf("the refusal does not name the command that fixes it: %v", err)
			}
		})
	}
}

func TestAMalformedAgentCatalogIsRefusedByShape(t *testing.T) {
	// Each of these otherwise surfaces later as a package missing entries,
	// rather than as the register being unreadable.
	directory := t.TempDir()
	for _, probe := range []struct{ name, body, wants string }{
		{"not JSON at all", "{not json", "invalid"},
		{"no agents key", `{"version": 1}`, "missing 'agents'"},
		{"agents is not an object", `{"agents": []}`, "not an object"},
		{"an entry is not an object", `{"agents": {"a": "definition"}}`, "not an object"},
	} {
		t.Run(probe.name, func(t *testing.T) {
			path := filepath.Join(directory, "catalog.json")
			if err := os.WriteFile(path, []byte(probe.body), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := rewriteAgentCatalog(path)
			if err == nil {
				t.Fatalf("accepted %s", probe.body)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(probe.wants)) {
				t.Errorf("refused for a different reason than this case is about.\n"+
					"wanted something naming %q, got: %v", probe.wants, err)
			}
		})
	}

	if _, err := rewriteAgentCatalog(filepath.Join(directory, "absent.json")); err == nil {
		t.Error("a missing register was accepted")
	}
}
