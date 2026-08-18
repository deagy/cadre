package guard

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The shell layer, compared against the Python it is ported from, before
// anything is stacked on top of it.
//
// Every handler's correctness depends on this producing the same segments and
// the same tokens. A divergence here surfaces as a handler that "does not
// fire", which sends a reader looking in entirely the wrong place.

// shellCorpus is the fixture's own commands plus shapes chosen to exercise the
// parts with a recorded history: line continuations, heredocs, quoted
// mentions, arithmetic shifts, escaped separators, and nesting.
func shellCorpus(t *testing.T) []string {
	t.Helper()
	commands := []string{
		`git reset --hard`,
		`git push \` + "\n" + ` origin main --force`,
		`cat > note.md <<'EOF'` + "\n" + `git reset --hard` + "\n" + `EOF`,
		`cat > f <<EOF && git worktree remove /x` + "\n" + `body` + "\n" + `EOF`,
		`echo "see <<EOF"; git reset --hard`,
		`echo $(( 1 << 2 )); git clean -fd`,
		`find . -exec git worktree remove {} \;`,
		`bash -c 'git reset --hard'`,
		`bash -c "git reset --hard"`,
		`env FOO=1 git reset --hard`,
		`git -C /somewhere reset --hard`,
		`git   reset    --hard   `,
		`echo 'a;b' && git clean -fdx`,
		`cat <<-TAB` + "\n" + "\tTAB" + "\n" + `git reset --hard`,
		`echo "unbalanced`,
		`git commit -m "a message with 'quotes' and \"escapes\""`,
		`printf '%s\n' one two | git apply -`,
		`git reset --hard; git clean -fd` + "\n" + `git push --force`,
		`cat <<''` + "\n" + "\n" + `git reset --hard`,
		`git checkout main -- src/`,
	}
	path := filepath.Join(repositoryRoot(t), "plugin", "tools", "guard_parity_fixture.json")
	if contents, err := os.ReadFile(path); err == nil {
		var document struct {
			Cases []struct {
				Command string `json:"command"`
			} `json:"cases"`
		}
		if json.Unmarshal(contents, &document) == nil {
			for _, testCase := range document.Cases {
				commands = append(commands, testCase.Command)
			}
		}
	}
	return commands
}

// pythonShellLayer asks the Python guard's own functions what they produce.
func pythonShellLayer(t *testing.T, commands []string) []struct {
	Segments []string   `json:"segments"`
	Tokens   [][]string `json:"tokens"`
} {
	t.Helper()
	script := `
import importlib.util, json, shlex, sys
spec = importlib.util.spec_from_file_location("g", sys.argv[1])
g = importlib.util.module_from_spec(spec); spec.loader.exec_module(g)
out = []
for command in json.load(sys.stdin):
    segments = g.split_top_level(command)
    tokens = []
    for segment in segments:
        try:
            tokens.append(shlex.split(segment, posix=True))
        except ValueError:
            tokens.append(None)
    out.append({"segments": segments, "tokens": tokens})
json.dump(out, sys.stdout)
`
	hook := filepath.Join(repositoryRoot(t), ".claude", "hooks", "guard_workspace_mutation.py")
	payload, err := json.Marshal(commands)
	if err != nil {
		t.Fatalf("marshalling the corpus: %v", err)
	}
	run := exec.Command("python3", "-c", script, hook)
	run.Stdin = strings.NewReader(string(payload))
	output, err := run.Output()
	if err != nil {
		t.Skipf("cannot consult the Python shell layer: %v", err)
	}
	var results []struct {
		Segments []string   `json:"segments"`
		Tokens   [][]string `json:"tokens"`
	}
	if err := json.Unmarshal(output, &results); err != nil {
		t.Fatalf("the Python side returned unusable output: %v", err)
	}
	return results
}

func TestTheShellLayerSplitsExactlyAsPythonDoes(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not available to compare against")
	}
	commands := shellCorpus(t)
	expected := pythonShellLayer(t, commands)
	if len(expected) != len(commands) {
		t.Fatalf("compared %d results against %d commands", len(expected), len(commands))
	}

	segmentDivergences, tokenDivergences := 0, 0
	for index, command := range commands {
		got := splitTopLevel(command)
		want := expected[index].Segments
		if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
			segmentDivergences++
			if segmentDivergences <= 6 {
				t.Errorf("segments differ for %q\n  python: %q\n  go:     %q", command, want, got)
			}
			continue
		}
		for position, segmentText := range got {
			goTokens, err := splitWords(segmentText)
			pythonTokens := expected[index].Tokens[position]
			if pythonTokens == nil {
				// Python refused this segment; Go must refuse it too, since a
				// segment one side skips and the other parses is a divergence
				// in what gets inspected at all.
				if err == nil {
					tokenDivergences++
					t.Errorf("python refused to tokenise %q; go produced %q", segmentText, goTokens)
				}
				continue
			}
			if err != nil {
				tokenDivergences++
				t.Errorf("go refused to tokenise %q, which python read as %q", segmentText, pythonTokens)
				continue
			}
			if strings.Join(goTokens, "\x00") != strings.Join(pythonTokens, "\x00") {
				tokenDivergences++
				if tokenDivergences <= 6 {
					t.Errorf("tokens differ for %q\n  python: %q\n  go:     %q",
						segmentText, pythonTokens, goTokens)
				}
			}
		}
	}
	t.Logf("compared %d commands: %d segment divergences, %d token divergences",
		len(commands), segmentDivergences, tokenDivergences)
}
