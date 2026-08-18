package guard

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The Cline guard implements the same rules in TypeScript, and the two must
// stay in step.
//
// This was plugin/tools/test_guard_parity.py, which compared the Python hook
// against cline-plugins/cline-agents/index.ts. The Python is gone; the
// TypeScript is not, and it is still a second implementation that a change to
// the rules has to reach. Without this, adding a handler on one side leaves
// Cline users with less enforcement and no signal at all.
//
// Both halves are kept because they fail differently. Structural parity
// catches a handler added to one file and not the other. Behavioural parity
// catches the divergences structure cannot see -- a truncating split, a `??`
// where the other file means `or`, a missing bounds check -- each of which
// leaves both files with identical tables and different meanings.

func clineGuardPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(repositoryRoot(t), "cline-plugins", "cline-agents", "index.ts")
}

func readClineGuard(t *testing.T) string {
	t.Helper()
	contents, err := os.ReadFile(clineGuardPath(t))
	if err != nil {
		t.Skipf("no Cline guard to compare against: %v", err)
	}
	return string(contents)
}

var keyDeclaration = regexp.MustCompile(`^["']?([A-Za-z_][A-Za-z0-9_]*)["']?\s*:`)

// typescriptObjectKeys returns the top-level keys of a `const <name> ... = {`
// object literal.
//
// A brace-matched scan rather than a regex over the whole file: the values are
// function references and arrow functions, and a regex trying to skip those is
// the kind of thing that quietly stops matching and then asserts nothing.
func typescriptObjectKeys(t *testing.T, source, declaration string) map[string]bool {
	t.Helper()
	opening := regexp.MustCompile(`const ` + regexp.QuoteMeta(declaration) + `\b[^=]*=\s*\{`)
	location := opening.FindStringIndex(source)
	if location == nil {
		t.Fatalf("the Cline guard has no `const %s = {` declaration", declaration)
	}
	start := location[1] - 1
	depth, end := 0, -1
	for index := start; index < len(source); index++ {
		switch source[index] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = index
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		t.Fatalf("unbalanced braces in the Cline guard's %s", declaration)
	}

	keys := map[string]bool{}
	depth = 0
	for _, line := range strings.Split(source[start+1:end], "\n") {
		trimmed := strings.TrimSpace(line)
		if depth == 0 {
			if match := keyDeclaration.FindStringSubmatch(trimmed); match != nil {
				keys[match[1]] = true
			}
		}
		depth += strings.Count(line, "{") + strings.Count(line, "(") + strings.Count(line, "[")
		depth -= strings.Count(line, "}") + strings.Count(line, ")") + strings.Count(line, "]")
	}
	return keys
}

var quotedString = regexp.MustCompile(`"([^"]*)"`)

// typescriptStringList returns the string literals in a `const <name> = new
// Set([...])` or `= [...]` initialiser.
func typescriptStringList(t *testing.T, source, declaration string) map[string]bool {
	t.Helper()
	pattern := regexp.MustCompile(`(?s)const ` + regexp.QuoteMeta(declaration) +
		`\b[^=]*=\s*(?:new Set\()?\[(.*?)\]`)
	match := pattern.FindStringSubmatch(source)
	if match == nil {
		t.Fatalf("the Cline guard has no `const %s = [...]` declaration", declaration)
	}
	values := map[string]bool{}
	for _, found := range quotedString.FindAllStringSubmatch(match[1], -1) {
		values[found[1]] = true
	}
	return values
}

func sortedKeys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func requireSameSet(t *testing.T, what string, mine, theirs map[string]bool, consequence string) {
	t.Helper()
	if len(mine) == 0 {
		t.Fatalf("%s: the Go side is empty, so this comparison asserts nothing", what)
	}
	if strings.Join(sortedKeys(mine), ",") == strings.Join(sortedKeys(theirs), ",") {
		return
	}
	var onlyMine, onlyTheirs []string
	for key := range mine {
		if !theirs[key] {
			onlyMine = append(onlyMine, key)
		}
	}
	for key := range theirs {
		if !mine[key] {
			onlyTheirs = append(onlyTheirs, key)
		}
	}
	sort.Strings(onlyMine)
	sort.Strings(onlyTheirs)
	t.Errorf("%s differs between the Go guard and the Cline guard.\n"+
		"  only in Go:    %v\n  only in Cline: %v\n\n%s",
		what, onlyMine, onlyTheirs, consequence)
}

func TestTheHandlerTablesMatchTheClineGuard(t *testing.T) {
	source := readClineGuard(t)
	mine := map[string]bool{}
	for name := range handlers {
		mine[name] = true
	}
	requireSameSet(t, "the set of guarded git subcommands", mine,
		typescriptObjectKeys(t, source, "GIT_GUARD_HANDLERS"),
		"Add the handler to both files in the same change, or Cline users get less "+
			"enforcement than Claude Code users with nothing reporting it.")
}

func TestTheWrapperTablesMatchTheClineGuard(t *testing.T) {
	source := readClineGuard(t)
	mine := map[string]bool{}
	for name := range wrapperFlagsWithValue {
		mine[name] = true
	}
	requireSameSet(t, "the set of stripped leading wrappers", mine,
		typescriptObjectKeys(t, source, "WRAPPER_FLAGS_WITH_VALUE"),
		"One guard strips a leading wrapper the other does not, so the same wrapped "+
			"command is blocked on one runner and allowed on the other.")
}

func TestTheGlobalFlagSetsMatchTheClineGuard(t *testing.T) {
	source := readClineGuard(t)
	requireSameSet(t, "the git global flags that consume a value", gitGlobalFlagsWithValue,
		typescriptStringList(t, source, "GIT_GLOBAL_FLAGS_WITH_VALUE"),
		"A flag one guard consumes and the other does not shifts which token reads "+
			"as the subcommand.")
}

func TestTheRecursionBoundsMatchTheClineGuard(t *testing.T) {
	source := readClineGuard(t)
	for _, bound := range []struct {
		mine        int
		declaration string
	}{
		{maxShellRecursionDepth, "MAX_SHELL_C_RECURSION_DEPTH"},
		{maxAliasExpansionDepth, "MAX_ALIAS_EXPANSION_DEPTH"},
	} {
		pattern := regexp.MustCompile(`const ` + bound.declaration + ` = (\d+);`)
		match := pattern.FindStringSubmatch(source)
		if match == nil {
			t.Errorf("the Cline guard has no %s constant", bound.declaration)
			continue
		}
		theirs, err := strconv.Atoi(match[1])
		if err != nil {
			t.Errorf("%s is not a number: %v", bound.declaration, err)
			continue
		}
		if theirs != bound.mine {
			t.Errorf("%s is %d in Go and %d in the Cline guard. A command nested "+
				"exactly at one bound is caught by one guard and not the other.",
				bound.declaration, bound.mine, theirs)
		}
	}
}

func TestTheGuardRegionMarkersArePresentAndOrdered(t *testing.T) {
	// The behavioural half slices this region out; without the markers it can
	// check nothing, and a silently-skipping parity check is worse than none.
	source := readClineGuard(t)
	begin := strings.Index(source, "// cadre:guard-region:begin")
	end := strings.Index(source, "// cadre:guard-region:end")
	if begin < 0 {
		t.Error("the Cline guard has no guard-region begin marker")
	}
	if end < 0 {
		t.Error("the Cline guard has no guard-region end marker")
	}
	if begin >= 0 && end >= 0 && begin >= end {
		t.Error("the Cline guard's region markers are out of order")
	}
}

// exitUnsupported is what guard_parity_runner.mjs returns when node cannot
// transpile the TypeScript region on this machine. Kept in sync with it.
const exitUnsupported = 3

func TestTheGoAndClineGuardsAgreeOnEveryFixtureCase(t *testing.T) {
	// The behavioural half. An unavailable toolchain SKIPS rather than fails --
	// a missing tool must not read as a red build -- but the skip says so,
	// rather than passing silently.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not available to run the Cline guard")
	}
	runner := filepath.Join(repositoryRoot(t), "plugin", "tools", "guard_parity_runner.mjs")
	if _, err := os.Stat(runner); err != nil {
		t.Skipf("no Cline parity runner: %v", err)
	}

	cases := loadFixture(t)
	type planCase struct {
		ID      string `json:"id"`
		Command string `json:"command"`
		Cwd     string `json:"cwd"`
	}
	plan := struct {
		Cases []planCase `json:"cases"`
	}{}
	goDecisions := map[string]*Decision{}

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
		plan.Cases = append(plan.Cases, planCase{ID: testCase.ID, Command: command, Cwd: cwd})
		goDecisions[testCase.ID] = EvaluateCommand(command, cwd)
	}

	planPath := filepath.Join(t.TempDir(), "plan.json")
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("building the plan: %v", err)
	}
	if err := os.WriteFile(planPath, encoded, 0o644); err != nil {
		t.Fatalf("writing the plan: %v", err)
	}

	run := exec.Command(node, runner, planPath)
	run.Dir = repositoryRoot(t)
	output, err := run.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == exitUnsupported {
			t.Skipf("this machine cannot transpile the Cline guard's TypeScript: %s",
				strings.TrimSpace(string(exit.Stderr)))
		}
		t.Fatalf("the Cline parity runner failed: %v", err)
	}
	var results struct {
		Results map[string]struct {
			Decision string `json:"decision"`
			Reason   string `json:"reason"`
		} `json:"results"`
	}
	if err := json.Unmarshal(output, &results); err != nil {
		t.Fatalf("the Cline runner returned unusable output: %v", err)
	}
	if len(results.Results) == 0 {
		t.Fatal("the Cline runner reported no decisions; this comparison checked nothing")
	}

	compared, blocked := 0, 0
	var divergences []string
	for id, theirs := range results.Results {
		mine := goDecisions[id]
		compared++
		goBlocked := mine != nil
		clineBlocked := theirs.Decision == "blocked"
		if goBlocked {
			blocked++
		}
		if goBlocked != clineBlocked {
			divergences = append(divergences, fmt.Sprintf(
				"%s: go=%v cline=%v", id, goBlocked, clineBlocked))
		}
	}
	if blocked == 0 {
		t.Fatal("no case was blocked; this comparison exercises nothing")
	}
	sort.Strings(divergences)
	t.Logf("compared %d cases against the Cline guard, %d blocked", compared, blocked)
	if len(divergences) > 0 {
		t.Errorf("%d case(s) diverge from the Cline guard:\n  %s",
			len(divergences), strings.Join(divergences, "\n  "))
	}
}
