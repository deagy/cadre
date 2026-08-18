package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The hook contract, as opposed to the decision logic the guard package's own
// differentials cover.
//
// Everything here is a way the hook can be handed something it did not expect.
// All of them must exit 0 and say nothing: a PreToolUse hook that errors or
// hangs blocks every Bash call in the session, which is how a guard gets
// switched off for good.

// buildGuard compiles the hook once for the tests below.
func buildGuard(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "cadre-guard")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the hook: %v\n%s", err, output)
	}
	return binary
}

// scratchRepo is a repository with one commit and an uncommitted edit, which
// is the state `git reset --hard` is refused in.
func scratchRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
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
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return root
}

func invoke(t *testing.T, binary, stdin string, environment ...string) (stdout string, code int) {
	t.Helper()
	command := exec.Command(binary)
	command.Stdin = strings.NewReader(stdin)
	command.Env = append(os.Environ(), environment...)
	var out strings.Builder
	command.Stdout = &out
	err := command.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return out.String(), exit.ExitCode()
	}
	if err != nil {
		t.Fatalf("running the hook: %v", err)
	}
	return out.String(), 0
}

func payload(t *testing.T, command, cwd string) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]string{"command": command},
		"cwd":             cwd,
	})
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	return string(encoded)
}

func TestADestructiveCommandIsDeniedWithAReason(t *testing.T) {
	binary, repo := buildGuard(t), scratchRepo(t)
	stdout, code := invoke(t, binary, payload(t, "git reset --hard", repo))
	if code != 0 {
		t.Errorf("the hook exited %d; a PreToolUse hook communicates through stdout, not "+
			"its exit code, and a non-zero exit blocks differently", code)
	}
	var decision struct {
		HookSpecificOutput struct {
			HookEventName            string `json:"hookEventName"`
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &decision); err != nil {
		t.Fatalf("the decision is not JSON: %v\n%s", err, stdout)
	}
	if decision.HookSpecificOutput.PermissionDecision != "deny" {
		t.Errorf("decision is %q, not deny", decision.HookSpecificOutput.PermissionDecision)
	}
	if decision.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Errorf("hookEventName is %q", decision.HookSpecificOutput.HookEventName)
	}
	// The reason reaches the model, so it has to say what to do instead rather
	// than only that something was refused.
	reason := decision.HookSpecificOutput.PermissionDecisionReason
	if !strings.Contains(reason, "stash") {
		t.Errorf("the reason offers no alternative:\n%s", reason)
	}
}

func TestEveryUnexpectedInputIsSilentAndExitsZero(t *testing.T) {
	binary, repo := buildGuard(t), scratchRepo(t)
	for _, testCase := range []struct {
		name  string
		stdin string
	}{
		{"empty input", ""},
		{"not JSON at all", "this is not json"},
		{"JSON that is not an object", `["a list"]`},
		{"a different hook event", `{"hook_event_name":"PostToolUse","tool_name":"Bash",` +
			`"tool_input":{"command":"git reset --hard"},"cwd":"` + repo + `"}`},
		{"a different tool", `{"hook_event_name":"PreToolUse","tool_name":"Edit",` +
			`"tool_input":{"command":"git reset --hard"},"cwd":"` + repo + `"}`},
		{"no command", `{"hook_event_name":"PreToolUse","tool_name":"Bash",` +
			`"tool_input":{},"cwd":"` + repo + `"}`},
		{"a whitespace command", payload(t, "   ", repo)},
		{"a command in a directory that does not exist", payload(t, "git reset --hard", "/nonexistent/xyz")},
	} {
		stdout, code := invoke(t, binary, testCase.stdin)
		if code != 0 {
			t.Errorf("%s: exited %d, which would disrupt every Bash call", testCase.name, code)
		}
		if strings.TrimSpace(stdout) != "" {
			t.Errorf("%s: said something instead of staying silent:\n%s", testCase.name, stdout)
		}
	}
}

func TestTheDisableSwitchIsHonouredBeforeAnythingElse(t *testing.T) {
	// Checked before stdin is read, so a wedged guard can always be stepped
	// past. Both accepted spellings are covered because a switch that only
	// half works is worse than none.
	binary, repo := buildGuard(t), scratchRepo(t)
	for _, value := range []string{"1", "true", "TRUE", " true "} {
		stdout, code := invoke(t, binary, payload(t, "git reset --hard", repo),
			disableEnvVar+"="+value)
		if code != 0 || strings.TrimSpace(stdout) != "" {
			t.Errorf("%s=%q did not disable the guard (exit %d, output %q)",
				disableEnvVar, value, code, stdout)
		}
	}
	// And a value that is not an opt-out must leave it on.
	stdout, _ := invoke(t, binary, payload(t, "git reset --hard", repo), disableEnvVar+"=no")
	if strings.TrimSpace(stdout) == "" {
		t.Errorf("%s=no switched the guard off; only 1/true do that", disableEnvVar)
	}
}
