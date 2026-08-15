package orchestration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The API runner executes tool calls a *model* chose. These tests are
// adversarial on purpose: each one is a way a model could reach something it
// was not given, and the assertion is that it cannot.

func projectWithFile(t *testing.T, relative, content string) string {
	t.Helper()
	root := t.TempDir()
	// EvalSymlinks up front: macOS /var is a symlink to /private/var, so an
	// unresolved root makes every containment comparison fail for a reason
	// that has nothing to do with the rule under test.
	resolved, err := filepath.EvalSymlinks(root)
	if err == nil {
		root = resolved
	}
	if relative != "" {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestPathsOutsideTheProjectAreRefused(t *testing.T) {
	root := projectWithFile(t, "src/app.go", "package main\n")

	for _, escape := range []string{
		"../outside.txt",
		"../../etc/passwd",
		"src/../../outside.txt",
		"/etc/passwd",
	} {
		if _, err := ResolveWithinProject(root, escape); err == nil {
			t.Errorf("path %q must be refused", escape)
		}
	}

	// ...and an ordinary path inside is not.
	if _, err := ResolveWithinProject(root, "src/app.go"); err != nil {
		t.Errorf("a path inside the project must resolve: %v", err)
	}
}

func TestASymlinkOutOfTheTreeIsCaughtByResolvingFirst(t *testing.T) {
	// The reason resolution happens before the containment check. The literal
	// path is entirely inside the project; only following it reveals that it
	// is not.
	root := projectWithFile(t, "src/app.go", "package main\n")
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "innocent.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := ResolveWithinProject(root, "innocent.txt"); err == nil {
		t.Error("a symlink pointing out of the project must be refused")
	}
}

func TestASymlinkedParentIsResolvedForAPathThatDoesNotExistYet(t *testing.T) {
	// write_file legitimately names a path that does not exist. Resolution
	// must still follow a symlinked *parent*, or a new file can be created
	// outside the project through a directory link.
	root := projectWithFile(t, "", "")
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := ResolveWithinProject(root, "escape/new-file.txt"); err == nil {
		t.Error("a not-yet-existing path under a symlinked parent must be refused")
	}
	// A genuinely new path inside the project still resolves.
	if _, err := ResolveWithinProject(root, "src/brand-new.txt"); err != nil {
		t.Errorf("a new path inside the project must resolve: %v", err)
	}
}

func TestTheGitDirectoryIsRefusedAtAnyDepth(t *testing.T) {
	// A hook is code that runs later, outside this loop and outside every
	// limit it applies. Rewriting history is not editing the project either.
	root := projectWithFile(t, "", "")
	for _, path := range []string{
		".git/config",
		".git/hooks/pre-commit",
		"nested/.git/config",
	} {
		if _, err := ResolveWithinProject(root, path); err == nil {
			t.Errorf("%q must be refused", path)
		} else if !strings.Contains(err.Error(), "git directory") {
			t.Errorf("%q refused for the wrong reason: %v", path, err)
		}
	}
	// A file merely *named* like it is fine -- the rule is about the
	// directory, not the substring.
	if _, err := ResolveWithinProject(root, "docs/gitignore-notes.md"); err != nil {
		t.Errorf("an ordinary path containing 'git' must resolve: %v", err)
	}
}

func TestMalformedPathsAreRefused(t *testing.T) {
	root := projectWithFile(t, "", "")
	for _, bad := range []string{"", "   ", "with\x00nul"} {
		if _, err := ResolveWithinProject(root, bad); err == nil {
			t.Errorf("path %q must be refused", bad)
		}
	}
}

func TestReadRefusesASymlinkAtTheFinalComponent(t *testing.T) {
	if !NoFollowSupported {
		t.Skip("platform cannot refuse a symlink at open time")
	}
	root := projectWithFile(t, "real.txt", "contents\n")
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(filepath.Join(root, "real.txt"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// Open-time refusal, not a pre-check: a symlink swapped in after a
	// containment check cannot redirect the read.
	if _, err := ReadFileCapped(link); err == nil {
		t.Error("reading through a symlink must be refused at open time")
	}
	if got, err := ReadFileCapped(filepath.Join(root, "real.txt")); err != nil || got != "contents\n" {
		t.Errorf("read = %q (%v), want the file's contents", got, err)
	}
}

func TestWriteRefusesASymlinkAtTheFinalComponent(t *testing.T) {
	// The case the Python original called out: containment is decided before
	// the write, and a symlink appearing in between would redirect it. Team
	// dispatch runs members concurrently against one project root, so the
	// interleaving is real.
	if !NoFollowSupported {
		t.Skip("platform cannot refuse a symlink at open time")
	}
	root := projectWithFile(t, "", "")
	outside := t.TempDir()
	target := filepath.Join(outside, "victim.txt")
	if err := os.WriteFile(target, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "innocent.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if err := WriteFileCapped(link, []byte("redirected\n")); err == nil {
		t.Error("writing through a symlink must be refused")
	}
	if contents, _ := os.ReadFile(target); string(contents) != "original\n" {
		t.Errorf("the symlink target was modified: %q", contents)
	}
}

func TestWriteRefusesANonRegularFile(t *testing.T) {
	root := projectWithFile(t, "", "")
	directory := filepath.Join(root, "a-directory")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileCapped(directory, []byte("x")); err == nil {
		t.Error("writing to a directory must be refused")
	}
}

func TestWritesAndReadsAreCapped(t *testing.T) {
	root := projectWithFile(t, "", "")
	if err := WriteFileCapped(filepath.Join(root, "big.txt"),
		make([]byte, MaxWriteBytes+1)); err == nil {
		t.Error("a write over the cap must be refused")
	}
	// At the cap exactly is allowed: the limit is a ceiling, not a fence one
	// byte short of it.
	if err := WriteFileCapped(filepath.Join(root, "atlimit.txt"),
		make([]byte, MaxWriteBytes)); err != nil {
		t.Errorf("a write exactly at the cap must succeed: %v", err)
	}

	oversized := filepath.Join(root, "huge.txt")
	if err := os.WriteFile(oversized, make([]byte, MaxReadBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFileCapped(oversized); err == nil {
		t.Error("a read over the cap must be refused rather than truncated")
	}
}

func TestAgentStartingCommandsAreNeverRunnable(t *testing.T) {
	// The refusal that cannot be configured away. A role able to start
	// another agent escapes every limit placed on it, by starting something
	// without them -- so this is checked *before* the operator's allowlist,
	// and an operator who allowlists one anyway still cannot grant it.
	for _, command := range []string{"cadre", "codex", "claude", "cline", "agentic-sdlc"} {
		if _, err := CheckCommandAllowed(command, []string{command}); err == nil {
			t.Errorf("%q must be refused even when allowlisted", command)
		} else if !strings.Contains(err.Error(), "never runnable") {
			t.Errorf("%q refused for the wrong reason: %v", command, err)
		}
	}
	// Case-folded: Codex is codex.
	if _, err := CheckCommandAllowed("Codex", []string{"Codex"}); err == nil {
		t.Error("the refusal must be case-insensitive")
	}
}

func TestOnlyAllowlistedCommandsRun(t *testing.T) {
	if _, err := CheckCommandAllowed("rm", nil); err == nil {
		t.Error("an empty allowlist must permit nothing")
	}
	if _, err := CheckCommandAllowed("rm", []string{"ls"}); err == nil {
		t.Error("a command outside the allowlist must be refused")
	}
	// Allowlisted but absent from PATH is refused too, and says which case it
	// is -- the two failures need different fixes.
	if _, err := CheckCommandAllowed("definitely-not-installed-xyz",
		[]string{"definitely-not-installed-xyz"}); err == nil {
		t.Error("an allowlisted command missing from PATH must be refused")
	} else if !strings.Contains(err.Error(), "not found on PATH") {
		t.Errorf("the refusal must distinguish missing from denied: %v", err)
	}
}

func TestToolAvailabilityFollowsAuthorization(t *testing.T) {
	readOnly := AvailableToolNames(nil, false, nil)
	for _, forbidden := range []string{"write_file", "edit_file", "run_command"} {
		for _, name := range readOnly {
			if name == forbidden {
				t.Errorf("%s must not be offered to a read-only role", forbidden)
			}
		}
	}

	// All three conditions: a declared write tier, writes allowed, and a
	// non-empty allowlist with Bash declared.
	full := AvailableToolNames([]string{"Edit", "Write", "Bash"}, true, []string{"ls"})
	has := func(want string) bool {
		for _, name := range full {
			if name == want {
				return true
			}
		}
		return false
	}
	if !has("write_file") || !has("edit_file") {
		t.Errorf("a write-authorized role must get the write tools: %v", full)
	}
	if !has("run_command") {
		t.Errorf("a configured allowlist must offer run_command: %v", full)
	}
	// An unconfigured allowlist means the capability is unavailable, not that
	// everything is permitted.
	for _, name := range AvailableToolNames([]string{"Edit", "Write", "Bash"}, true, nil) {
		if name == "run_command" {
			t.Error("run_command must not appear without a configured allowlist")
		}
	}

	// The third gate, found missing by probe_api_runner_parity.py: a role
	// whose declared tier has no Edit/Write never reaches the write tools,
	// however the operator has configured things.
	for _, name := range AvailableToolNames([]string{"Read"}, true, []string{"ls"}) {
		if name == "write_file" || name == "edit_file" || name == "run_command" {
			t.Errorf("%s offered to a role whose tier declares no write capability", name)
		}
	}
	// ...and Bash specifically gates run_command, separately from writing.
	for _, name := range AvailableToolNames([]string{"Edit", "Write"}, true, []string{"ls"}) {
		if name == "run_command" {
			t.Error("run_command needs a declared Bash capability, not just an allowlist")
		}
	}
}

func TestToolResultsAreTruncatedVisibly(t *testing.T) {
	// A silently truncated result reads as a complete one, and the model
	// answers from a half-file without knowing.
	got := TruncateToolResult(strings.Repeat("x", MaxToolResultBytes+100))
	if len(got) <= MaxToolResultBytes {
		t.Error("a truncated result must still carry its marker")
	}
	if !strings.Contains(got, "truncated") {
		t.Error("truncation must be visible in the result")
	}
	if short := TruncateToolResult("small"); short != "small" {
		t.Errorf("an ordinary result must pass through unchanged: %q", short)
	}
}
