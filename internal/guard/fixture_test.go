package guard

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The shared behavioural fixture, replayed against this implementation and
// against the Python one it is ported from.
//
// plugin/tools/guard_parity_fixture.json already pins the Python hook and the
// TypeScript mirror in cline-plugins/cline-agents/index.ts to the same
// OUTCOMES rather than merely the same structure. This makes Go a third
// participant in the same corpus, on the same disposable repositories.
//
// Two assertions per case, and they are not the same: the decision must match
// what the fixture declares, and it must match what Python actually returns.
// The first catches a port that is wrong; the second catches a port that is
// wrong in the same way the fixture is.

type fixtureStep struct {
	Op         string `json:"op"`
	Path       string `json:"path"`
	Content    string `json:"content"`
	Name       string `json:"name"`
	Ref        string `json:"ref"`
	Branch     string `json:"branch"`
	Definition string `json:"definition"`
}

type fixtureCase struct {
	ID             string        `json:"id"`
	Why            string        `json:"why"`
	Setup          []fixtureStep `json:"setup"`
	Command        string        `json:"command"`
	Cwd            string        `json:"cwd"`
	Expected       string        `json:"expected"`
	ReasonContains string        `json:"reason_contains"`
	WrapInBashC    int           `json:"wrap_in_bash_c"`
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Dir(filepath.Dir(working))
}

func loadFixture(t *testing.T) []fixtureCase {
	t.Helper()
	path := filepath.Join(repositoryRoot(t), "plugin", "tools", "guard_parity_fixture.json")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read the shared fixture: %v", err)
	}
	var document struct {
		Cases []fixtureCase `json:"cases"`
	}
	if err := json.Unmarshal(contents, &document); err != nil {
		t.Fatalf("the fixture does not parse: %v", err)
	}
	if len(document.Cases) == 0 {
		t.Fatal("the fixture declares no cases; this harness would pass vacuously")
	}
	return document.Cases
}

// fixtureWorld is one disposable repository built from a case's setup steps.
type fixtureWorld struct {
	t         *testing.T
	root      string
	repo      string
	worktrees map[string]string
}

func newFixtureWorld(t *testing.T) *fixtureWorld {
	t.Helper()
	root := t.TempDir()
	world := &fixtureWorld{t: t, root: root, repo: filepath.Join(root, "repo"),
		worktrees: map[string]string{}}
	if err := os.MkdirAll(world.repo, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	world.git(world.repo, "init", "-q", "-b", "main")
	world.git(world.repo, "config", "user.email", "test@example.com")
	world.git(world.repo, "config", "user.name", "Test User")
	return world
}

func (w *fixtureWorld) git(cwd string, args ...string) {
	w.t.Helper()
	command := exec.Command("git", args...)
	command.Dir = cwd
	if output, err := command.CombinedOutput(); err != nil {
		w.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func (w *fixtureWorld) write(relative, content string) {
	w.t.Helper()
	path := filepath.Join(w.repo, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		w.t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		w.t.Fatalf("write %s: %v", relative, err)
	}
}

func (w *fixtureWorld) apply(step fixtureStep) {
	w.t.Helper()
	switch step.Op {
	case "commit":
		w.write(step.Path, step.Content)
		w.git(w.repo, "add", step.Path)
		w.git(w.repo, "commit", "-q", "-m", "write "+step.Path)
	case "dirty", "untracked":
		w.write(step.Path, step.Content)
	case "mkdir":
		if err := os.MkdirAll(filepath.Join(w.repo, filepath.FromSlash(step.Path)), 0o755); err != nil {
			w.t.Fatalf("mkdir: %v", err)
		}
	case "branch":
		w.git(w.repo, "branch", step.Name)
	case "branch-at":
		// update-ref, not `branch -f`: this suite runs under the very guard it
		// tests when a developer drives it by hand.
		w.git(w.repo, "update-ref", "refs/heads/"+step.Name, step.Ref)
	case "worktree":
		path := filepath.Join(w.repo, ".worktrees", step.Name)
		w.git(w.repo, "worktree", "add", "-q", path, "-b", step.Branch)
		w.worktrees[step.Name] = path
	case "detach-worktree":
		moved := filepath.Join(w.root, step.Name+"-relocated")
		if err := os.Rename(w.worktrees[step.Name], moved); err != nil {
			w.t.Fatalf("relocating a worktree: %v", err)
		}
	case "age-worktree":
		// git gc prunes a worktree registration only once its admin files are
		// older than gc.worktreePruneExpire (3.months.ago by default), which
		// is why the gc handler probes at that expiry rather than prune's own.
		admin := filepath.Join(w.repo, ".git", "worktrees", step.Name)
		old := time.Now().Add(-365 * 24 * time.Hour)
		_ = filepath.Walk(admin, func(path string, _ os.FileInfo, err error) error {
			if err == nil {
				_ = os.Chtimes(path, old, old)
			}
			return nil
		})
		_ = os.Chtimes(admin, old, old)
	case "config-alias":
		w.git(w.repo, "config", "alias."+step.Name, step.Definition)
	default:
		w.t.Fatalf("unknown fixture setup op: %q -- a typo must not pass quietly", step.Op)
	}
}

func (w *fixtureWorld) resolve(text string) string {
	text = strings.ReplaceAll(text, "{repo}", w.repo)
	text = strings.ReplaceAll(text, "{tmp}", w.root)
	for name, path := range w.worktrees {
		text = strings.ReplaceAll(text, "{wt:"+name+"}", path)
	}
	return text
}

// shellQuote wraps a script for `bash -c '...'`, matching how the Python
// harness quotes it so both guards are handed the same string.
func shellQuote(script string) string {
	return "'" + strings.ReplaceAll(script, "'", `'\''`) + "'"
}

// pythonDecision runs the Python hook over the same payload.
func pythonDecision(t *testing.T, command, cwd string) (blocked bool, reason string, ok bool) {
	t.Helper()
	hook := filepath.Join(repositoryRoot(t), ".claude", "hooks", "guard_workspace_mutation.py")
	if _, err := os.Stat(hook); err != nil {
		return false, "", false
	}
	payload, err := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]string{"command": command},
		"cwd":             cwd,
	})
	if err != nil {
		t.Fatalf("building the payload: %v", err)
	}
	run := exec.Command("python3", hook)
	run.Stdin = strings.NewReader(string(payload))
	output, err := run.Output()
	if err != nil {
		return false, "", false
	}
	text := strings.TrimSpace(string(output))
	if text == "" {
		return false, "", true
	}
	var decision struct {
		HookSpecificOutput struct {
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(text), &decision); err != nil {
		return false, "", false
	}
	return decision.HookSpecificOutput.PermissionDecision == "deny",
		decision.HookSpecificOutput.PermissionDecisionReason, true
}

func TestTheSharedFixtureAgreesWithTheGoGuard(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	cases := loadFixture(t)

	var mismatches []string
	agreed := 0
	for _, testCase := range cases {
		world := newFixtureWorld(t)
		for _, step := range testCase.Setup {
			world.apply(step)
		}
		command := world.resolve(testCase.Command)
		for i := 0; i < testCase.WrapInBashC; i++ {
			command = "bash -c " + shellQuote(command)
		}
		cwd := world.repo
		if testCase.Cwd != "" {
			cwd = world.resolve(testCase.Cwd)
		}

		decision := EvaluateCommand(command, cwd)
		got := "allowed"
		reason := ""
		if decision != nil {
			got, reason = "blocked", decision.Reason
		}
		if got != testCase.Expected {
			mismatches = append(mismatches, fmt.Sprintf("%s: expected %s, got %s\n      why: %s\n      cmd: %s",
				testCase.ID, testCase.Expected, got, testCase.Why, command))
			continue
		}
		if testCase.ReasonContains != "" && !strings.Contains(reason, testCase.ReasonContains) {
			mismatches = append(mismatches, fmt.Sprintf("%s: blocked, but the reason omits %q\n      reason: %s",
				testCase.ID, testCase.ReasonContains, reason))
			continue
		}
		agreed++
	}
	t.Logf("%d of %d fixture cases agree with the Go guard", agreed, len(cases))
	if len(mismatches) > 0 {
		t.Errorf("%d of %d fixture case(s) disagree:\n    %s",
			len(mismatches), len(cases), strings.Join(mismatches, "\n    "))
	}
}

func TestTheGoAndPythonGuardsAgreeOnEveryFixtureCase(t *testing.T) {
	// The differential. The fixture says what should happen; this says the two
	// implementations do the same thing, which also catches a port that is
	// wrong in exactly the way the fixture is.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not available to compare against")
	}
	cases := loadFixture(t)

	compared := 0
	var divergences []string
	for _, testCase := range cases {
		world := newFixtureWorld(t)
		for _, step := range testCase.Setup {
			world.apply(step)
		}
		command := world.resolve(testCase.Command)
		for i := 0; i < testCase.WrapInBashC; i++ {
			command = "bash -c " + shellQuote(command)
		}
		cwd := world.repo
		if testCase.Cwd != "" {
			cwd = world.resolve(testCase.Cwd)
		}

		pythonBlocked, pythonReason, ok := pythonDecision(t, command, cwd)
		if !ok {
			continue // the Python side could not be consulted for this case
		}
		compared++
		decision := EvaluateCommand(command, cwd)
		goBlocked := decision != nil
		if goBlocked != pythonBlocked {
			divergences = append(divergences, fmt.Sprintf(
				"%s: python=%v go=%v\n      cmd: %s", testCase.ID, pythonBlocked, goBlocked, command))
			continue
		}
		if goBlocked && decision.Reason != pythonReason {
			divergences = append(divergences, fmt.Sprintf(
				"%s: both blocked, different reasons\n      python: %s\n      go:     %s",
				testCase.ID, pythonReason, decision.Reason))
		}
	}
	if compared == 0 {
		t.Fatal("the Python guard answered no case; this differential compared nothing")
	}
	t.Logf("compared %d of %d cases against the Python guard", compared, len(cases))
	if len(divergences) > 0 {
		t.Errorf("%d case(s) diverge from the Python guard:\n    %s",
			len(divergences), strings.Join(divergences, "\n    "))
	}
}
