package generators

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The destructive-git guard reaches everyone who installs the plugin.
//
// This repository runs a PreToolUse hook that refuses destructive git
// operations. The main `cadre` plugin must wire the same one, so a project
// that installs it gets the same structural protection -- not just this
// checkout, and not only projects that also install a lifecycle plugin.
//
// The failure is quiet in the way that matters: the plugin installs, every
// command works, and the guard simply is not there. Nothing errors, because
// nothing asked for it.
//
// The script is byte-identical to the one this repository runs on itself.
// There is exactly one script to review, fanned out at package time -- two
// copies would mean the reviewed one and the shipped one can differ.
//
// Ported from test_repository_health.py's
// test_main_plugin_packages_destructive_git_guard_hook (deagy/cadre#129).

func TestThePackagedPluginWiresTheDestructiveGitGuard(t *testing.T) {
	packageRoot, repoRoot := freshPackage(t)

	packagedSelector := filepath.Join(packageRoot, "hooks", "guard")
	packagedHooks := filepath.Join(packageRoot, "hooks", "hooks.json")
	sourceSelector := filepath.Join(repoRoot, "hooks", "guard")

	shipped, err := os.ReadFile(packagedSelector)
	if err != nil {
		t.Fatalf("the plugin ships no destructive-git guard selector: %v", err)
	}
	source, err := os.ReadFile(sourceSelector)
	if err != nil {
		t.Fatalf("this repository has no guard selector to compare against: %v", err)
	}
	if !bytes.Equal(shipped, source) {
		t.Errorf("the packaged guard selector has drifted from hooks/guard "+
			"(%d bytes vs %d).\n"+
			"There is meant to be one script to review, fanned out at package "+
			"time; two copies means the reviewed one and the shipped one can differ.",
			len(shipped), len(source))
	}
	// The binaries it selects between are checked separately, by behaviour
	// rather than by bytes -- see guard_binaries_test.go for why comparing
	// compiled output would report drift that is only a toolchain difference.

	raw, err := os.ReadFile(packagedHooks)
	if err != nil {
		t.Fatalf("the plugin ships no hooks.json: %v", err)
	}
	var manifest struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("hooks.json does not parse: %v", err)
	}

	preToolUse, present := manifest.Hooks["PreToolUse"]
	if !present || len(preToolUse) == 0 {
		t.Fatal("hooks.json wires no PreToolUse hook, so the guard never runs")
	}
	matchedBash := false
	invokesGuard := false
	for _, entry := range preToolUse {
		if entry.Matcher != "Bash" {
			continue
		}
		matchedBash = true
		for _, hook := range entry.Hooks {
			// The guard only helps if it is the script being run. Matched on
			// the filename rather than the whole command, since the plugin-root
			// prefix is substituted at install time.
			if strings.Contains(hook.Command, "hooks/guard") {
				invokesGuard = true
			}
		}
	}
	if !matchedBash {
		t.Error("no PreToolUse entry matches Bash; a guard that does not see " +
			"shell commands cannot refuse a destructive git operation")
	}
	if !invokesGuard {
		t.Errorf("the Bash PreToolUse entry does not invoke hooks/guard:\n%s", string(raw))
	}
}

func TestThePackagedGuardIsInvokedThroughThePluginRoot(t *testing.T) {
	// The command runs on the installer's machine, where this checkout does
	// not exist. A path that resolved here would be a hook that silently never
	// fires -- Claude Code cannot run a script that is not there, and a
	// missing hook is not an error.
	packageRoot, _ := freshPackage(t)
	raw, err := os.ReadFile(filepath.Join(packageRoot, "hooks", "hooks.json"))
	if err != nil {
		t.Skipf("no packaged hooks.json: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "${CLAUDE_PLUGIN_ROOT}") {
		t.Errorf("the packaged hook command does not resolve through "+
			"${CLAUDE_PLUGIN_ROOT}:\n%s", text)
	}
	// And no absolute path from this machine leaked in alongside it. The
	// packaging-safety test covers the whole tree; this is the one file where
	// the consequence is a guard that never runs.
	if strings.Contains(text, "/home/") || strings.Contains(text, "C:\\") {
		t.Errorf("an absolute path leaked into the packaged hook command:\n%s", text)
	}
}
