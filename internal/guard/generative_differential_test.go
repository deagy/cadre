package guard

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A generated space, not just the fixture.
//
// The shared fixture has 61 hand-written cases for a guard with nine handlers,
// a shell parser, wrapper stripping, alias expansion and bounded recursion.
// Those cases reach the shapes someone thought to write down; this reaches the
// combinations nobody did.
//
// Every command is run through both implementations against the same
// repository state. What is compared is the decision AND the exact reason,
// because two guards agreeing on "blocked" would hide a ref one of them
// silently truncated -- the divergence class the fixture's reason_contains
// exists for.

// repoState is one disposable repository shape, built once and reused for
// every generated command, since git subprocesses dominate the runtime.
type repoState struct {
	name  string
	build func(w *fixtureWorld)
}

var repoStates = []repoState{
	{"clean", func(w *fixtureWorld) {
		w.apply(fixtureStep{Op: "commit", Path: "a.txt", Content: "one"})
	}},
	{"dirty", func(w *fixtureWorld) {
		w.apply(fixtureStep{Op: "commit", Path: "a.txt", Content: "one"})
		w.apply(fixtureStep{Op: "dirty", Path: "a.txt", Content: "changed"})
	}},
	{"dirty-with-untracked", func(w *fixtureWorld) {
		w.apply(fixtureStep{Op: "commit", Path: "a.txt", Content: "one"})
		w.apply(fixtureStep{Op: "dirty", Path: "a.txt", Content: "changed"})
		w.apply(fixtureStep{Op: "untracked", Path: "junk.tmp", Content: "x"})
		w.apply(fixtureStep{Op: "mkdir", Path: "sub"})
	}},
	{"branches-and-worktree", func(w *fixtureWorld) {
		w.apply(fixtureStep{Op: "commit", Path: "a.txt", Content: "one"})
		w.apply(fixtureStep{Op: "commit", Path: "b.txt", Content: "two"})
		w.apply(fixtureStep{Op: "branch", Name: "existing"})
		w.apply(fixtureStep{Op: "branch-at", Name: "behind", Ref: "HEAD~1"})
		w.apply(fixtureStep{Op: "worktree", Name: "wt1", Branch: "wtbranch"})
		w.apply(fixtureStep{Op: "dirty", Path: "a.txt", Content: "changed"})
	}},
}

// generatedCommands builds the space: each git invocation, under each prefix,
// in each spelling that the parser has a separate path for.
func generatedCommands() []string {
	invocations := []string{
		"git reset --hard", "git reset --hard HEAD", "git reset --hard behind",
		"git reset --hard HEAD~1", "git reset --soft HEAD~1", "git reset",
		"git clean -f", "git clean -fd", "git clean -fdx", "git clean -n", "git clean -fn",
		"git clean -d", "git clean --force", "git clean --dry-run --force",
		"git branch -D existing", "git branch -d existing", "git branch --delete --force existing",
		"git branch -f -d existing", "git branch existing2", "git branch -D",
		"git push --force", "git push -f origin main", "git push --force-with-lease",
		"git push --force-with-lease=main", "git push --delete origin main",
		"git push origin :main", "git push origin main", "git push -d origin main",
		"git checkout existing", "git checkout behind", "git checkout -b fresh",
		"git checkout -B existing", "git checkout -Bexisting", "git checkout -fB existing",
		"git checkout -Bf existing", "git checkout -B existing behind",
		"git checkout HEAD -- a.txt", "git checkout -- a.txt", "git checkout behind -- a.txt",
		"git checkout behind a.txt", "git checkout a.txt", "git checkout",
		"git switch existing", "git switch -c fresh2", "git switch -C existing",
		"git switch -Cexisting", "git switch -fC existing", "git switch --force-create existing",
		"git switch --orphan lonely", "git switch -d", "git switch --detach",
		"git restore a.txt", "git restore --source=behind a.txt", "git restore -s behind a.txt",
		"git restore --source behind -- a.txt", "git restore --staged a.txt",
		"git worktree remove .worktrees/wt1", "git worktree move .worktrees/wt1 /tmp/x",
		"git worktree prune", "git worktree prune -n", "git worktree prune --dry-run",
		"git worktree prune --expire now", "git worktree list", "git worktree lock x",
		"git worktree add /tmp/new -b fresh3", "git worktree add -B existing /tmp/new",
		"git worktree add -B existing /tmp/new behind", "git worktree",
		"git gc", "git gc --prune=now", "git gc --aggressive",
		"git status", "git log --oneline", "git stash push",
	}
	prefixes := []string{
		"", "sudo ", "env FOO=1 ", "timeout 10 ", "nice -n 5 ", "setsid ",
		"env -u BAR ", "timeout -s KILL 5 ", "taskset -c 0,1 ", "xargs -n 1 ",
		"sudo timeout 10 ", "FOO=1 BAR=2 ",
	}
	globals := []string{"", "-C sub ", "-C sub -C .. ", "-c core.pager=cat "}

	seen := map[string]bool{}
	var commands []string
	add := func(command string) {
		if !seen[command] {
			seen[command] = true
			commands = append(commands, command)
		}
	}
	for _, invocation := range invocations {
		for _, prefix := range prefixes {
			add(prefix + invocation)
		}
		for _, globalFlag := range globals {
			if globalFlag == "" {
				continue
			}
			add(strings.Replace(invocation, "git ", "git "+globalFlag, 1))
		}
	}
	// Shapes the parser has dedicated paths for, applied to a representative
	// destructive invocation rather than to all of them.
	for _, shape := range []string{
		`bash -c '%s'`, `sh -c "%s"`, `bash -lc '%s'`, `bash -eu -c '%s'`,
		`bash -c 'bash -c "%s"'`, `bash -c 'bash -c "bash -c \"%s\""'`,
		`echo hello && %s`, `echo hello; %s`, `true || %s`, `echo x | %s`,
		"echo first\n%s", `find . -maxdepth 0 -exec %s \;`,
		`cat > note.md <<'EOF'` + "\n%s\n" + `EOF`,
		`cat > note.md <<EOF && %s` + "\nbody\nEOF",
		`echo "quoted <<EOF"; %s`, `echo $(( 1 << 2 )); %s`,
		"%s \\\n  --quiet",
	} {
		for _, invocation := range []string{
			"git reset --hard", "git clean -fd", "git worktree remove .worktrees/wt1",
			"git push --force", "git checkout -B existing",
		} {
			add(fmt.Sprintf(shape, invocation))
		}
	}
	// Alias expansion, including the config-carrying and shell forms.
	for _, alias := range []string{
		`git -c alias.wtr='worktree remove' wtr .worktrees/wt1`,
		`git -c alias.rh='reset --hard' rh`,
		`git -c alias.a=b -c alias.b='reset --hard' a`,
		`git -c alias.loop=loop loop`,
		`git -c alias.sh='!git reset --hard' sh`,
		`git -c alias.g='-c gc.worktreePruneExpire=now gc' g`,
		`git -c alias.status='reset --hard' status`,
		`git -c gc.worktreePruneExpire=now gc`,
	} {
		add(alias)
	}
	return commands
}

// pythonDecisions asks the Python guard about every command in one process,
// which keeps this differential to seconds rather than minutes.
func pythonDecisions(t *testing.T, commands []string, cwd string) []struct {
	Blocked bool   `json:"blocked"`
	Reason  string `json:"reason"`
} {
	t.Helper()
	script := `
import importlib.util, json, sys
spec = importlib.util.spec_from_file_location("g", sys.argv[1])
g = importlib.util.module_from_spec(spec); spec.loader.exec_module(g)
cwd = sys.argv[2]
out = []
for command in json.load(sys.stdin):
    try:
        decision = g.evaluate_command(command, cwd)
    except Exception as exc:
        out.append({"blocked": False, "reason": f"<raised {exc!r}>"})
        continue
    out.append({"blocked": bool(decision), "reason": decision["reason"] if decision else ""})
json.dump(out, sys.stdout)
`
	hook := filepath.Join(repositoryRoot(t), ".claude", "hooks", "guard_workspace_mutation.py")
	payload, err := json.Marshal(commands)
	if err != nil {
		t.Fatalf("marshalling the space: %v", err)
	}
	run := exec.Command("python3", "-c", script, hook, cwd)
	run.Stdin = strings.NewReader(string(payload))
	output, err := run.Output()
	if err != nil {
		t.Skipf("cannot consult the Python guard: %v", err)
	}
	var results []struct {
		Blocked bool   `json:"blocked"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal(output, &results); err != nil {
		t.Fatalf("the Python side returned unusable output: %v", err)
	}
	return results
}

func TestTheGoAndPythonGuardsAgreeAcrossAGeneratedSpace(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not available to compare against")
	}
	commands := generatedCommands()
	if len(commands) < 400 {
		t.Fatalf("the generated space is only %d commands; that is not a space", len(commands))
	}

	total, blocked, divergent := 0, 0, 0
	for _, state := range repoStates {
		world := newFixtureWorld(t)
		state.build(world)
		expected := pythonDecisions(t, commands, world.repo)
		if len(expected) != len(commands) {
			t.Fatalf("%s: %d results for %d commands", state.name, len(expected), len(commands))
		}
		for index, command := range commands {
			total++
			decision := EvaluateCommand(command, world.repo)
			goBlocked := decision != nil
			if goBlocked {
				blocked++
			}
			if strings.HasPrefix(expected[index].Reason, "<raised") {
				t.Errorf("%s: the Python guard raised on %q: %s",
					state.name, command, expected[index].Reason)
				continue
			}
			if goBlocked != expected[index].Blocked {
				divergent++
				if divergent <= 10 {
					t.Errorf("%s: %q\n  python blocked=%v\n  go     blocked=%v",
						state.name, command, expected[index].Blocked, goBlocked)
				}
				continue
			}
			if goBlocked && decision.Reason != expected[index].Reason {
				divergent++
				if divergent <= 10 {
					t.Errorf("%s: %q -- both blocked, reasons differ\n  python: %s\n  go:     %s",
						state.name, command, expected[index].Reason, decision.Reason)
				}
			}
		}
	}
	// A space that blocks nothing would agree trivially.
	if blocked == 0 {
		t.Fatal("no generated command was blocked by either guard; the space exercises nothing")
	}
	t.Logf("compared %d command/state pairs across %d states: %d blocked, %d divergent",
		total, len(repoStates), blocked, divergent)
}
