package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// slopRepoRoot points CADRE_REPO_ROOT at this checkout so the shipped rules
// are found whatever the working directory is, and returns the root.
func slopRepoRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	if _, err := os.Stat(filepath.Join(root, slopRulesRelativePath)); err != nil {
		t.Skipf("no roster/shared/slop-rules.yaml beside this package: %v", err)
	}
	t.Setenv("CADRE_REPO_ROOT", root)
	return root
}

func slopRun(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runSlop(context.Background(), args, strings.NewReader(stdin), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func writeSlopFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// AC-1 through the command: exit 1 on a tell, 0 on clean text, 2 on misuse.
func TestSlopScanExitCodes(t *testing.T) {
	slopRepoRoot(t)
	dir := t.TempDir()
	slop := writeSlopFile(t, dir, "slop.md", "Fast \u2014 and small.\n")
	clean := writeSlopFile(t, dir, "clean.md", "Fast and small.\n")

	cases := []struct {
		name  string
		stdin string
		args  []string
		want  int
	}{
		{"tell found", "", []string{"scan", slop}, 1},
		{"clean file", "", []string{"scan", clean}, 0},
		{"tell on stdin", "Fast \u2014 and small.\n", []string{"scan", "-"}, 1},
		{"clean stdin", "Fast and small.\n", []string{"scan", "-"}, 0},
		{"flags after path", "", []string{"scan", slop, "--format", "json"}, 1},
		{"no verb", "", nil, 2},
		{"no inputs", "", []string{"scan"}, 2},
		{"unknown verb", "", []string{"bogus"}, 2},
		{"fix is not built", "", []string{"fix", slop}, 2},
		{"baseline is not built", "", []string{"baseline"}, 2},
		{"bad format", "", []string{"scan", "--format", "xml", clean}, 2},
		{"bad surface", "", []string{"scan", "--surface", "code-comment", clean}, 2},
		{"unknown flag", "", []string{"scan", "--hook", clean}, 2},
		{"missing file", "", []string{"scan", filepath.Join(dir, "absent.md")}, 2},
		{"bad git range", "", []string{"scan", "--git-range", "--output=x"}, 2},
		{"help", "", []string{"scan", "--help"}, 0},
		{"verb help", "", []string{"--help"}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if code, _, stderr := slopRun(t, c.stdin, c.args...); code != c.want {
				t.Errorf("exit %d, want %d (stderr: %s)", code, c.want, stderr)
			}
		})
	}
}

func TestSlopScanJSONOutput(t *testing.T) {
	slopRepoRoot(t)
	dir := t.TempDir()
	slop := writeSlopFile(t, dir, "slop.md", "Intro.\n\nFast \u2014 and small.\n")

	code, stdout, stderr := slopRun(t, "", "scan", "--format", "json", slop)
	if code != 1 || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	var report struct {
		Failed bool `json:"failed"`
		Units  []struct {
			File string `json:"file"`
			Hits []struct {
				File    string `json:"file"`
				Line    int    `json:"line"`
				Rule    string `json:"rule"`
				Class   string `json:"class"`
				Span    string `json:"span"`
				Context string `json:"context"`
			} `json:"hits"`
		} `json:"units"`
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if !report.Failed || len(report.Units) != 1 || len(report.Units[0].Hits) != 1 {
		t.Fatalf("report = %+v", report)
	}
	hit := report.Units[0].Hits[0]
	if hit.File != slop || hit.Line != 3 || hit.Rule != "em-dash" || hit.Class != "hard" ||
		hit.Span != "\u2014" || hit.Context != "Fast \u2014 and small." {
		t.Errorf("hit = %+v", hit)
	}
}

func TestSlopScanTextOutput(t *testing.T) {
	slopRepoRoot(t)
	code, stdout, _ := slopRun(t, "Fast \u2014 and small.\n", "scan", "-")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"stdin: 1 tell(s)", "stdin:1:", "[hard/em-dash]", "not evidence of authorship"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}
}

// AC-10: a malformed rules file fails closed with a clear error and exit 2,
// never an empty or partial rule set that reports "clear".
func TestSlopScanMalformedRulesFailClosed(t *testing.T) {
	rules := map[string]string{
		"not yaml":     "version: [1\n",
		"no rules":     "version: 1\nrules: []\n",
		"bad pattern":  "version: 1\nrules:\n  - id: a\n    class: hard\n    pattern: '(?=x)'\n    rationale: r\n    example: x\n    clean: y\n",
		"unknown key":  "version: 1\nrules:\n  - id: a\n    class: hard\n    pattern: x\n    rationale: r\n    example: x\n    clean: y\n    flags: i\n",
		"empty":        "",
		"wrong shape":  "- just\n- a list\n",
		"no clean":     "version: 1\nrules:\n  - id: a\n    class: hard\n    pattern: x\n    rationale: r\n    example: x\n",
		"bad class":    "version: 1\nrules:\n  - id: a\n    class: maybe\n    pattern: x\n    rationale: r\n    example: x\n    clean: y\n",
		"dupe ids":     "version: 1\nrules:\n  - {id: a, class: hard, pattern: x, rationale: r, example: x, clean: y}\n  - {id: a, class: hard, pattern: x, rationale: r, example: x, clean: y}\n",
		"zero version": "version: 0\nrules:\n  - {id: a, class: hard, pattern: x, rationale: r, example: x, clean: y}\n",
	}
	for name, content := range rules {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeSlopFile(t, root, slopRulesRelativePath, content)
			t.Setenv("CADRE_REPO_ROOT", root)
			code, stdout, stderr := slopRun(t, "Fast and small.\n", "scan", "-")
			if code != 2 {
				t.Fatalf("exit %d, want 2; stdout: %s", code, stdout)
			}
			if !strings.Contains(stderr, "slop rules") || !strings.Contains(stderr, slopRulesFilename) {
				t.Errorf("error should name the rules file: %q", stderr)
			}
			if strings.Contains(stdout, "clear") {
				t.Errorf("a broken rules file must never report clear: %q", stdout)
			}
		})
	}
}

// The scanner reads the rules file at the path every other roster/shared file
// uses: a rule changed there changes the verdict, with no built-in copy.
func TestSlopScanReadsRulesFromRosterShared(t *testing.T) {
	root := t.TempDir()
	writeSlopFile(t, root, slopRulesRelativePath,
		"version: 7\nrules:\n  - {id: only-banana, class: hard, pattern: banana, rationale: r, example: banana, clean: apple}\n")
	t.Setenv("CADRE_REPO_ROOT", root)

	if code, _, stderr := slopRun(t, "a banana\n", "scan", "-"); code != 1 {
		t.Errorf("custom rule should fire: exit %d %s", code, stderr)
	}
	// The em dash is a default rule, absent from this file, so it must not hit.
	if code, stdout, _ := slopRun(t, "fast \u2014 small\n", "scan", "-"); code != 0 || !strings.Contains(stdout, "rules v7") {
		t.Errorf("no built-in fallback rules expected: exit %d\n%s", code, stdout)
	}
}

func overlayProject(t *testing.T, overlay string) string {
	t.Helper()
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSlopFile(t, project, filepath.Join(".agents", "shared", slopRulesFilename), overlay)
	return project
}

func TestSlopScanDisablesRulesByIDWithReason(t *testing.T) {
	slopRepoRoot(t)
	text := "Fast \u2014 and small.\n"

	project := overlayProject(t, "disabled:\n  - id: em-dash\n    reason: legal appendix uses them\n")
	code, stdout, stderr := slopRun(t, text, "scan", "--project", project, "-")
	if code != 0 {
		t.Fatalf("disabled rule should not fail: exit %d\n%s%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Disabled rules:") || !strings.Contains(stdout, "em-dash: legal appendix uses them") {
		t.Errorf("the report must show the disabled rule and its reason:\n%s", stdout)
	}

	code, jsonOut, _ := slopRun(t, text, "scan", "--format", "json", "--project", project, "-")
	if code != 0 || !strings.Contains(jsonOut, `"disabled_rules"`) || !strings.Contains(jsonOut, "legal appendix uses them") {
		t.Errorf("json must carry the disabled rule: exit %d\n%s", code, jsonOut)
	}

	// Rejected overlays fail closed with exit 2.
	bad := map[string]string{
		"missing reason":   "disabled:\n  - id: em-dash\n",
		"empty reason":     "disabled:\n  - id: em-dash\n    reason: ' '\n",
		"unknown rule":     "disabled:\n  - id: no-such-rule\n    reason: x\n",
		"extra field":      "disabled:\n  - id: em-dash\n    reason: x\n    until: never\n",
		"not a list":       "disabled: em-dash\n",
		"custom rule list": "rules:\n  - {id: mine, class: hard, pattern: x, rationale: r, example: x, clean: y}\n",
		"changed version":  "version: 99\n",
	}
	for name, overlay := range bad {
		t.Run(name, func(t *testing.T) {
			project := overlayProject(t, overlay)
			if code, _, stderr := slopRun(t, text, "scan", "--project", project, "-"); code != 2 {
				t.Errorf("exit %d, want 2 (stderr: %s)", code, stderr)
			}
		})
	}
}

// AC-4 through the command: --git-range reports the commit SHA.
func TestSlopScanGitRange(t *testing.T) {
	slopRepoRoot(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{
			"-c", "user.name=Test", "-c", "user.email=test@example.test", "-c", "commit.gpgsign=false",
		}, args...)...)
		command.Dir = repo
		command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "chore: start")
	git("tag", "base")
	git("commit", "-q", "--allow-empty", "-m", "fix: parser", "-m", "A short and plain body.")
	t.Chdir(repo)

	code, stdout, stderr := slopRun(t, "", "scan", "--git-range", "base..HEAD")
	if code != 0 {
		t.Fatalf("clean range: exit %d\n%s%s", code, stdout, stderr)
	}

	git("commit", "-q", "--allow-empty", "-m", "feat: importer \u2014 now faster")
	sha := git("rev-parse", "HEAD")
	code, stdout, _ = slopRun(t, "", "scan", "--git-range", "base..HEAD", "--format", "json")
	if code != 1 {
		t.Fatalf("slop commit: exit %d\n%s", code, stdout)
	}
	if !strings.Contains(stdout, `"commit": "`+sha+`"`) || !strings.Contains(stdout, `"rule": "em-dash"`) {
		t.Errorf("hit must carry the commit SHA %s:\n%s", sha, stdout)
	}

	if code, _, _ := slopRun(t, "", "scan", "--git-range", "nope..HEAD"); code != 2 {
		t.Errorf("an unknown revision is an I/O error, exit 2; got %d", code)
	}
}

func TestSlopScanCommitSurfaceOnAFile(t *testing.T) {
	slopRepoRoot(t)
	dir := t.TempDir()
	message := writeSlopFile(t, dir, "COMMIT_EDITMSG", "docs: tidy\n\nGreat question about retries.\n")
	if code, stdout, _ := slopRun(t, "", "scan", "--surface", "commit", message); code != 1 || !strings.Contains(stdout, "sycophancy") {
		t.Errorf("commit message file should fail on sycophancy: exit %d\n%s", code, stdout)
	}
}

func TestSlopIsWiredIntoTheDispatcher(t *testing.T) {
	root := filepath.Dir(filepath.Dir(mustGetwd(t)))
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"help"}, Deps{
		Stdout: &stdout, Stderr: &stderr, RepoRoot: root,
		SubcommandsPath: filepath.Join(root, SubcommandsTableRelativePath),
	})
	if code != 0 || !strings.Contains(stdout.String(), "slop") {
		t.Errorf("cadre help must list slop (exit %d)", code)
	}
}
