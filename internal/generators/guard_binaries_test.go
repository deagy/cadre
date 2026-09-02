package generators

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The committed guard binaries must be current, and the packaged plugin must
// carry all of them.
//
// The guard ships compiled, one per platform, so the PreToolUse hook never
// depends on a network fetch. It fails OPEN by design, which is exactly why:
// a binary that could not be downloaded would remove the protection silently,
// on an offline machine or a first run, and nothing would report it.
//
// That trade buys a different hazard. A committed binary is a copy of source
// that can go stale, and a stale guard is one that enforces last week's rules
// while the tree says otherwise. Nothing about it looks wrong.
//
// Checked by BEHAVIOUR, not by bytes. `go build` is only byte-reproducible
// when the toolchain matches, so comparing the committed binary against a
// fresh one would fail for anyone whose Go differs from the committer's --
// a drift guard that reports drift when there is none gets ignored, and then
// reports nothing.

// expectedGuardBinaries is every platform the contract promises. Kept here
// rather than derived from the directory, so a binary going missing is a
// failure rather than a smaller list that still passes.
var expectedGuardBinaries = []string{
	"cadre-guard-linux-amd64",
	"cadre-guard-linux-arm64",
	"cadre-guard-darwin-amd64",
	"cadre-guard-darwin-arm64",
	"cadre-guard-windows-amd64.exe",
}

func TestEveryContractedPlatformHasACommittedGuardBinary(t *testing.T) {
	root := repositoryRoot(t)
	for _, name := range expectedGuardBinaries {
		path := filepath.Join(root, "hooks", "bin", name)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("hooks/bin/%s is missing: %v. Run `make guard-binaries`. "+
				"A platform with no binary is a platform with no guard, and the "+
				"selector exits silently rather than reporting it.", name, err)
			continue
		}
		if info.Size() < 500_000 {
			t.Errorf("hooks/bin/%s is only %d bytes, which is not a built binary",
				name, info.Size())
		}
		// The selector runs it directly, so the executable bit is load-bearing
		// on every platform that has one.
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
			t.Errorf("hooks/bin/%s is not executable, so the selector will skip it "+
				"and the guard will never run", name)
		}
	}
}

func TestTheCommittedGuardBinaryMatchesTheSource(t *testing.T) {
	// Behavioural staleness check: the committed binary for THIS platform is
	// asked the same questions as a binary built from the working tree, and
	// must answer identically. That catches a source change nobody rebuilt for
	// without depending on byte reproducibility.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	// This builds the guard from source below, so it needs the Go toolchain
	// as much as it needs git. Guarding one and not the other made the test
	// fail rather than skip on a machine with no Go installed -- a property
	// of the machine, not a defect in the guard.
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("the Go toolchain is not available")
	}
	root := repositoryRoot(t)
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	committed := filepath.Join(root, "hooks", "bin",
		"cadre-guard-"+runtime.GOOS+"-"+runtime.GOARCH+suffix)
	if _, err := os.Stat(committed); err != nil {
		t.Skipf("no committed guard binary for %s/%s: %v", runtime.GOOS, runtime.GOARCH, err)
	}

	fresh := filepath.Join(t.TempDir(), "cadre-guard-fresh"+suffix)
	build := exec.Command("go", "build", "-o", fresh, "./cmd/cadre-guard")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the guard from source: %v\n%s", err, output)
	}

	repo := scratchGuardRepo(t)
	// Shapes spanning the handlers, the wrapper stripping and the shell
	// recursion, so a change to any of them shows up here.
	commands := []string{
		"git reset --hard",
		"git status",
		"git clean -fd",
		"git worktree remove /somewhere",
		"git push --force",
		"git branch -D existing",
		"git checkout -B existing",
		"sudo timeout 10 git reset --hard",
		`bash -c 'git reset --hard'`,
		`find . -maxdepth 0 -exec git worktree remove /x \;`,
		`git -c alias.rh='reset --hard' rh`,
	}
	differing := 0
	for _, command := range commands {
		committedOut := askGuard(t, committed, command, repo)
		freshOut := askGuard(t, fresh, command, repo)
		if committedOut != freshOut {
			differing++
			t.Errorf("the committed binary disagrees with the source for %q.\n"+
				"  committed: %s\n  source:    %s\n\n"+
				"Run `make guard-binaries` and commit the result.",
				command, truncate(committedOut), truncate(freshOut))
		}
	}
	// A comparison where nothing was ever blocked would agree trivially.
	blocked := 0
	for _, command := range commands {
		if askGuard(t, committed, command, repo) != "" {
			blocked++
		}
	}
	if blocked == 0 {
		t.Fatal("the committed binary blocked none of these commands; either it is " +
			"not the guard or this comparison exercises nothing")
	}
	t.Logf("compared %d commands, %d blocked, %d differing", len(commands), blocked, differing)
}

func truncate(text string) string {
	if len(text) > 120 {
		return text[:120] + "..."
	}
	if text == "" {
		return "<allowed>"
	}
	return text
}

// askGuard runs one binary over one command and returns its decision, or "".
func askGuard(t *testing.T, binary, command, cwd string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]string{"command": command},
		"cwd":             cwd,
	})
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	run := exec.Command(binary)
	run.Stdin = strings.NewReader(string(payload))
	output, err := run.Output()
	if err != nil {
		t.Fatalf("running %s: %v", filepath.Base(binary), err)
	}
	return strings.TrimSpace(string(output))
}

// scratchGuardRepo is a repository with an uncommitted edit and a branch, the
// state most of the refusals above need.
func scratchGuardRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.invalid")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("add", "a.txt")
	run("commit", "-q", "-m", "one")
	run("branch", "existing")
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return root
}

func TestThePackagedPluginCarriesTheSelectorAndEveryBinary(t *testing.T) {
	packageRoot, _ := freshPackage(t)
	selector := filepath.Join(packageRoot, "hooks", "guard")
	info, err := os.Stat(selector)
	if err != nil {
		t.Fatalf("the packaged plugin ships no guard selector: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		t.Error("the packaged selector is not executable, so the hook cannot run it")
	}
	for _, name := range expectedGuardBinaries {
		if _, err := os.Stat(filepath.Join(packageRoot, "hooks", "bin", name)); err != nil {
			t.Errorf("the packaged plugin is missing hooks/bin/%s: %v", name, err)
		}
	}
	// And the hook command names the selector rather than an interpreter.
	raw, err := os.ReadFile(filepath.Join(packageRoot, "hooks", "hooks.json"))
	if err != nil {
		t.Fatalf("no packaged hooks.json: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "hooks/guard") {
		t.Errorf("the packaged hook does not invoke hooks/guard:\n%s", text)
	}
	if strings.Contains(text, "python") {
		t.Errorf("the packaged hook still runs an interpreter:\n%s", text)
	}
}
