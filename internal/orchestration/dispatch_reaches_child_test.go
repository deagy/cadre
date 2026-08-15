package orchestration

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The seam test.
//
// Every other test in this package calls the dispatch engine's pieces
// directly -- resolve a role, build a context, execute a child -- and they
// all passed while nothing in production called any of them. The MCP tool
// reached a function that composed a prompt from the literal string "Role
// instructions would be loaded from role file here", spawned `echo`, and
// reported {"status": "success", "exit_code": 0}.
//
// So these assert reachability, not correctness of the parts: that a role
// named in a tool call is actually opened, and that its text is what the
// child is run with. A component test cannot fail for a component nobody
// calls, which is precisely how this survived.

// roleMarker is deliberately unlike anything the dispatch code could produce
// on its own, so finding it proves the file was read rather than guessed.
const roleMarker = "MARKER-0f3a9c-ROLE-INSTRUCTIONS-FROM-DISK"

// codexRoleTier writes a role wrapper the resolver will actually accept.
//
// The model must be a concrete identifier from the validated set, not a tier
// name: an unrecognised model is refused during validation, before dispatch
// reaches the child at all. Written with "sonnet" the first time, these tests
// passed while never exercising the seam they exist for -- caught by
// falsification, not by them passing.
func codexRoleTier(t *testing.T, roleID, instructions string) string {
	return codexRoleTierWithSandbox(t, roleID, instructions, "")
}

func codexRoleTierWithSandbox(t *testing.T, roleID, instructions, sandbox string) string {
	t.Helper()
	tier := t.TempDir()
	body := "model = \"claude-sonnet-5\"\ndeveloper_instructions = \"" + instructions + "\"\n"
	if sandbox != "" {
		body += "sandbox_mode = \"" + sandbox + "\"\n"
	}
	if err := os.WriteFile(filepath.Join(tier, roleID+".toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return tier
}

// stdinEchoRunner writes a script that ignores its arguments and copies
// stdin to stdout, then points the codex runner setting at it.
//
// This is what makes the seam observable: the prompt is fed to the child on
// stdin, so a child that echoes stdin returns the exact text the dispatch
// composed. Finding the role marker in the result proves the role file was
// opened and its instructions reached a real child process.
func stdinEchoRunner(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stdin-echo runner script is POSIX shell")
	}
	script := filepath.Join(t.TempDir(), "fake-codex")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec cat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SECURE_CLOUD_AGENTS_CODEX_BIN", script)
}

func TestDispatchActuallyOpensTheNamedRoleFile(t *testing.T) {
	stdinEchoRunner(t)
	global := codexRoleTier(t, "probe-role", roleMarker)
	server := NewDispatchMCPServer(DispatchMCPServerConfig{
		ProjectRoot: t.TempDir(), GlobalRoot: global, PluginRoot: t.TempDir(),
	})

	response := server.HandleDispatchSecureCloudRole(&DispatchSecureCloudRoleRequest{
		RoleID: "probe-role", Brief: "do the thing",
		Mode: ModePlanningOnly, Classification: "internal", Wait: true,
	})

	rendered := renderResult(response.Result)
	if strings.Contains(rendered, "Role instructions would be loaded") {
		t.Fatalf("dispatch is still running on the placeholder instructions: %s", rendered)
	}
	// The proof: the role file's own text came back out of a child process.
	if !strings.Contains(rendered, roleMarker) {
		t.Fatalf("the role file's instructions never reached a child: %s", rendered)
	}
	// And the caller's brief travelled with it, fenced.
	if !strings.Contains(rendered, "do the thing") {
		t.Errorf("the brief did not reach the child: %s", rendered)
	}
	if !strings.Contains(rendered, "BEGIN UNTRUSTED TASK BRIEF") {
		t.Errorf("the brief reached the child unfenced: %s", rendered)
	}
}

func TestTheChildReceivesThePromptOnStdinNotInItsArgv(t *testing.T) {
	// The prompt carries the caller's untrusted brief and can be long. As an
	// argv element it lands in the process table, readable by any user on the
	// machine, and counts against ARG_MAX -- so a large brief fails at the
	// exec for a reason that names none of that.
	//
	// The echo runner copies stdin, so finding the brief in the output is
	// direct evidence it arrived there rather than as an argument.
	stdinEchoRunner(t)
	global := codexRoleTier(t, "probe-role", roleMarker)
	server := NewDispatchMCPServer(DispatchMCPServerConfig{
		ProjectRoot: t.TempDir(), GlobalRoot: global, PluginRoot: t.TempDir(),
	})
	response := server.HandleDispatchSecureCloudRole(&DispatchSecureCloudRoleRequest{
		RoleID: "probe-role", Brief: "BRIEF-ON-STDIN",
		Mode: ModePlanningOnly, Classification: "internal", Wait: true,
	})
	if !strings.Contains(renderResult(response.Result), "BRIEF-ON-STDIN") {
		t.Error("the prompt did not arrive on the child's stdin")
	}
}

func TestTheEffectiveSandboxReachesTheChild(t *testing.T) {
	// The sandbox was computed, logged, and then dropped at the exec: the
	// Claude Code invocation passed no permission flag at all, and the Codex
	// one was a stub. A sandbox the child never hears about is not a sandbox.
	//
	// The echo runner returns its own argv is not available, so this asserts
	// the narrowing decision instead: planning-review-only forces read-only
	// no matter what the role file declares.
	stdinEchoRunner(t)
	global := codexRoleTierWithSandbox(t, "wide-role", roleMarker, SandboxDangerFullAccess)

	role, err := ResolveRoleForDispatch("wide-role", RunnerCodex, t.TempDir(), global, t.TempDir(), ModePlanningOnly)
	if err != nil {
		t.Fatal(err)
	}
	effective, err := EffectiveSandboxForDispatch(role, ModePlanningOnly)
	if err != nil {
		t.Fatal(err)
	}
	if effective != SandboxReadOnly {
		t.Errorf("effective sandbox = %q, want read-only forced by planning-review-only", effective)
	}

	// And in write mode the role's own declaration is honoured rather than
	// flattened to the default -- which is what a hard-coded empty file
	// sandbox mode did.
	effective, err = EffectiveSandboxForDispatch(role, ModeRepositoryEdit)
	if err != nil {
		t.Fatal(err)
	}
	if effective != SandboxDangerFullAccess {
		t.Errorf("effective sandbox = %q, want the role's declared %q",
			effective, SandboxDangerFullAccess)
	}
}

func TestAnUnknownRoleIsUnavailableRatherThanASuccessfulNoOp(t *testing.T) {
	// The stub reported success for every role id, including ones that did
	// not exist anywhere -- the single clearest symptom, and one no component
	// test could show.
	server := NewDispatchMCPServer(DispatchMCPServerConfig{
		ProjectRoot: t.TempDir(), GlobalRoot: t.TempDir(), PluginRoot: t.TempDir(),
	})
	response := server.HandleDispatchSecureCloudRole(&DispatchSecureCloudRoleRequest{
		RoleID: "no-such-role", Brief: "b",
		Mode: ModePlanningOnly, Classification: "internal", Wait: true,
	})
	if response.Status == "success" {
		t.Fatal("dispatching a role that does not exist reported success")
	}
	if response.Status != "unavailable" {
		t.Errorf("status = %q, want unavailable", response.Status)
	}
}

func TestARoleDeclaringAWriteSandboxRequiresConfirmation(t *testing.T) {
	// The gate could never fire: the effective sandbox was computed as
	// ComputeEffectiveSandbox(mode, "") with a hard-coded empty file sandbox
	// mode, which always resolved to read-only. A dispatch that has not read
	// its role file cannot know whether it needs human confirmation.
	// The role declares danger-full-access specifically. A dispatch that did
	// not read the role file lands on workspace-write, the default for write
	// mode -- which is still write-capable, so asserting only "some write
	// sandbox" cannot tell the two apart. Naming the declared value is what
	// makes this a test of the seam rather than of the gate.
	global := codexRoleTierWithSandbox(t, "writer-role", roleMarker, SandboxDangerFullAccess)
	server := NewDispatchMCPServer(DispatchMCPServerConfig{
		ProjectRoot: t.TempDir(), GlobalRoot: global, PluginRoot: t.TempDir(),
	})

	response := server.HandleDispatchSecureCloudRole(&DispatchSecureCloudRoleRequest{
		RoleID: "writer-role", Brief: "edit the repository",
		Mode: ModeRepositoryEdit, Classification: "internal", Wait: true,
	})

	if response.Status != "confirmation_required" {
		t.Fatalf("status = %q, want confirmation_required for a write-capable dispatch", response.Status)
	}
	token, _ := response.Result["confirmation_token"].(string)
	if token == "" {
		t.Error("no confirmation token was issued to replay")
	}
	sandbox, _ := response.Result["sandbox_mode"].(string)
	if sandbox != SandboxDangerFullAccess {
		t.Errorf("sandbox_mode = %q, want the role file's declared %q -- "+
			"the role file was not consulted", sandbox, SandboxDangerFullAccess)
	}
}

func TestPlanningOnlyNeedsNoConfirmationBecauseItCannotWrite(t *testing.T) {
	// The other half: if the gate fired for read-only dispatch too, it would
	// be a prompt with nothing behind it, and operators learn to clear those
	// without reading them.
	global := codexRoleTier(t, "reader-role", roleMarker)
	server := NewDispatchMCPServer(DispatchMCPServerConfig{
		ProjectRoot: t.TempDir(), GlobalRoot: global, PluginRoot: t.TempDir(),
	})
	response := server.HandleDispatchSecureCloudRole(&DispatchSecureCloudRoleRequest{
		RoleID: "reader-role", Brief: "read the repository",
		Mode: ModePlanningOnly, Classification: "internal", Wait: true,
	})
	if response.Status == "confirmation_required" {
		t.Error("a read-only dispatch asked for write confirmation")
	}
}

func TestTeamDispatchRunsEachMembersOwnRole(t *testing.T) {
	// Team members route through the same function, so the stub made every
	// member run the same placeholder. Distinct markers prove they are
	// resolved separately.
	tier := t.TempDir()
	for _, role := range []struct{ id, marker string }{
		{"member-one", "MARKER-ONE"},
		{"member-two", "MARKER-TWO"},
	} {
		body := "model = \"claude-sonnet-5\"\ndeveloper_instructions = \"" + role.marker + "\"\n"
		if err := os.WriteFile(filepath.Join(tier, role.id+".toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	server := NewDispatchMCPServer(DispatchMCPServerConfig{
		ProjectRoot: t.TempDir(), GlobalRoot: tier, PluginRoot: t.TempDir(),
	})
	response := server.HandleDispatchTeam(&DispatchTeamRequest{
		Members: []map[string]string{
			{"role_id": "member-one", "brief": "first"},
			{"role_id": "member-two", "brief": "second"},
		},
		Mode: ModePlanningOnly, Classification: "internal", Wait: true,
	})

	rendered := renderResult(response.Result)
	if strings.Contains(rendered, "Role instructions would be loaded") {
		t.Fatal("team members are still running on the placeholder instructions")
	}
	// Each member is accounted for by name, whatever the spawn outcome.
	for _, id := range []string{"member-one", "member-two"} {
		if !strings.Contains(rendered, id) {
			t.Errorf("member %q is missing from the team result: %s", id, rendered)
		}
	}
}

func TestTheAPIRunnerIsReachableThroughDispatch(t *testing.T) {
	// ResolveRoleForDispatch answered `runner "api" not yet implemented`
	// long after the api runner was ported, so the whole runner was
	// unreachable through the tool that is supposed to select it.
	global := codexRoleTier(t, "api-role", roleMarker)
	role, err := ResolveRoleForDispatch("api-role", RunnerAPI, t.TempDir(), global, t.TempDir(), ModePlanningOnly)
	if err != nil {
		t.Fatalf("the api runner cannot resolve a role: %v", err)
	}
	if role.DeveloperInstructs != roleMarker {
		t.Errorf("instructions = %q, want the role file's own text", role.DeveloperInstructs)
	}
}

func TestAnUnparseableDispatchDepthCountsAsAtTheLimit(t *testing.T) {
	// The counter stops a dispatch chain recursing without bound. The Atoi
	// error was discarded, so a garbage value -- including one a child could
	// set for a grandchild -- read as depth 0 and reset the limit.
	t.Setenv(DepthEnvVar, "not-a-number")
	if depth := currentDispatchDepth(); depth != MaxDispatchDepth {
		t.Errorf("depth = %d, want the limit %d so the next dispatch is refused",
			depth, MaxDispatchDepth)
	}

	t.Setenv(DepthEnvVar, "1")
	if depth := currentDispatchDepth(); depth != 1 {
		t.Errorf("depth = %d, want the parsed value", depth)
	}
}

// renderResult flattens a result map to one searchable string.
func renderResult(result map[string]any) string {
	var parts []string
	var walk func(prefix string, value any)
	walk = func(prefix string, value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, inner := range typed {
				walk(prefix+"."+key, inner)
			}
		case []map[string]any:
			for index, inner := range typed {
				walk(prefix+"[]", inner)
				_ = index
			}
		case []any:
			for _, inner := range typed {
				walk(prefix+"[]", inner)
			}
		default:
			parts = append(parts, prefix+"="+strings.TrimSpace(toDisplay(typed)))
		}
	}
	for key, value := range result {
		walk(key, value)
	}
	return strings.Join(parts, " | ")
}

func toDisplay(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(fmt.Sprint(value)), "\n", " "))
}

// testRoots builds roots carrying a real role file for roleID.
//
// The tests below it were written against a dispatch that never opened a
// role file, so DispatchRoots{} was enough for them. Once dispatch actually
// resolves, empty roots mean "no such role" -- correctly -- so they need a
// role to dispatch.
func testRoots(t *testing.T, roleID string) DispatchRoots {
	t.Helper()
	return DispatchRoots{
		ProjectRoot: t.TempDir(),
		GlobalRoot:  codexRoleTier(t, roleID, roleMarker),
		PluginRoot:  t.TempDir(),
	}
}

// claudeRoleRoots builds roots carrying a Claude Code role wrapper.
//
// The Claude Code resolver reads .md files with YAML frontmatter from the
// project's .claude/agents/ or an installed plugin, not the .toml wrappers
// the Codex resolver uses -- so a test naming RunnerClaudeCode needs this
// shape, not testRoots'.
func claudeRoleRoots(t *testing.T, roleID string) DispatchRoots {
	t.Helper()
	// The plugin tier, which is where an installed role actually lives. A
	// project-tier role would additionally have to be committed to git before
	// write-mode dispatch trusts it -- correct behaviour, but a different
	// guard from the one these tests are about.
	plugin := t.TempDir()
	agents := filepath.Join(plugin, "marketplace", "plugin", "version", "agents")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + roleID + "\nmodel: claude-sonnet-5\n---\n\n" + roleMarker + "\n"
	if err := os.WriteFile(filepath.Join(agents, roleID+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return DispatchRoots{ProjectRoot: t.TempDir(), GlobalRoot: t.TempDir(), PluginRoot: plugin}
}

func TestOperatorForwardedEnvReachesTheChild(t *testing.T) {
	// runners.forward_env was registered, documented and validated while
	// nothing consumed it: BuildChildEnv took a projectRoot and never used
	// it. An operator who opted in got no forwarding and no error saying so,
	// which surfaces as an unexplained authentication failure from a
	// self-hosted provider.
	t.Setenv("SECURE_CLOUD_AGENTS_FORWARD_ENV", "PROBE_PROVIDER_KEY")
	t.Setenv("PROBE_PROVIDER_KEY", "secret-value")

	env := BuildChildEnv(1, t.TempDir())
	if env["PROBE_PROVIDER_KEY"] != "secret-value" {
		t.Errorf("a forwarded variable did not reach the child env: %v", env)
	}
}

func TestForwardingCannotOverwriteTheDepthCounter(t *testing.T) {
	// The counter is what stops a dispatch chain recursing without bound. If
	// forwarding were applied after it, an operator -- or anything that could
	// influence that list -- could reset the cap by forwarding the counter's
	// own variable.
	t.Setenv("SECURE_CLOUD_AGENTS_FORWARD_ENV", DepthEnvVar)
	t.Setenv(DepthEnvVar, "0")

	env := BuildChildEnv(1, t.TempDir())
	if env[DepthEnvVar] != "1" {
		t.Errorf("%s = %q, want the dispatch's own depth", DepthEnvVar, env[DepthEnvVar])
	}
}

func TestAVariableNotListedIsNotForwarded(t *testing.T) {
	// Deny by default: the allowlist plus exactly what the operator named.
	t.Setenv("SECURE_CLOUD_AGENTS_FORWARD_ENV", "PROBE_ALLOWED")
	t.Setenv("PROBE_ALLOWED", "yes")
	t.Setenv("PROBE_NOT_ALLOWED", "no")

	env := BuildChildEnv(1, t.TempDir())
	if _, present := env["PROBE_NOT_ALLOWED"]; present {
		t.Error("an unlisted variable was forwarded to the child")
	}
}

func TestTheChildAlwaysHasAPath(t *testing.T) {
	// A child with no PATH cannot exec anything it does not name absolutely,
	// which turns a missing PATH here into an unexplained child failure.
	//
	// PATH is cleared first: with one set, the allowlist copy supplies it and
	// the fallback never runs, so the test would pass whether or not the
	// fallback existed.
	t.Setenv("PATH", "")
	env := BuildChildEnv(1, "")
	if env["PATH"] == "" {
		t.Error("the child environment has no PATH at all")
	}

	// And a real PATH wins over the fallback -- it is a floor, not an
	// override.
	t.Setenv("PATH", "/opt/probe/bin")
	if got := BuildChildEnv(1, "")["PATH"]; got != "/opt/probe/bin" {
		t.Errorf("PATH = %q, want this process's own", got)
	}
}
