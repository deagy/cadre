package kernel

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The command layer, asserted directly rather than by comparison.
//
// Every entry point in this kernel was covered only by a differential against
// the Python kernel: "does it agree" rather than "does it do the right thing".
// That is a fine gate while both exist and no test at all once one of them
// goes -- measured, deleting the Python kernel took twenty CLI functions from
// full coverage to zero.
//
// So these say what the command layer owes a caller, in terms that outlive the
// thing it was ported from:
//
//   - a missing required argument is a usage error (exit 2), and names every
//     argument that is missing rather than the first;
//   - a value outside a declared choice set is a usage error, not something
//     the kernel attempts and then refuses;
//   - a flag that changes the world is never the default;
//   - an exit code distinguishes "you typed it wrong" (2) from "this cannot be
//     done" (1) from "somebody needs to look at this" (2 with a report), which
//     is the whole of what a script can act on.

// runCLI invokes the kernel exactly as the binary does.
func runCLI(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr)
	return code, stdout.String() + stderr.String()
}

func TestEveryCommandRefusesAnInvocationMissingItsRequiredArguments(t *testing.T) {
	// A usage error, and one that names *every* missing argument. An operator
	// fixing them one at a time learns about the next only after another
	// failed run, which for a command that talks to a forge means another
	// round trip to find out.
	for _, probe := range []struct {
		command string
		args    []string
		missing []string
	}{
		{"status", nil, []string{"--task-id"}},
		{"decide", []string{"--task-id", "T"},
			[]string{"--gate", "--role", "--decision", "--actor-id", "--evidence-uri"}},
		{"create-gate-issues", nil, []string{"--task-id", "--project-path", "--as-bot"}},
		{"create-github-gate-issues", nil, []string{"--task-id", "--repo", "--as-bot"}},
		{"publish-gate-status", nil, []string{"--task-id", "--forge", "--as-bot"}},
		{"publish-reviewer-nudge", nil, []string{"--task-id", "--repo", "--pr", "--as-bot"}},
		{"request-gate-reviewers", nil, []string{"--task-id", "--repo", "--pr", "--as-bot"}},
		{"request-gate-reviewers-gitlab", nil,
			[]string{"--task-id", "--project-path", "--mr-iid", "--as-bot"}},
		{"approve-from-github", nil, []string{"--task-id", "--gate", "--role", "--repo",
			"--pr", "--review-id", "--reviewer-login", "--commit-sha"}},
		{"approve-from-github-pr", nil, []string{"--task-id", "--gate", "--role", "--repo", "--pr"}},
		{"approve-from-gitlab", nil, []string{"--task-id", "--gate", "--role", "--project-path",
			"--mr-iid", "--approval-id", "--approver-username", "--commit-sha"}},
		{"approve-from-gitlab-mr", nil,
			[]string{"--task-id", "--gate", "--role", "--project-path", "--mr-iid"}},
		{"link-intent-from-gitlab-issue", nil,
			[]string{"--task-id", "--role", "--project-path", "--issue-iid"}},
		{"link-requirements-from-github-issue", nil,
			[]string{"--task-id", "--role", "--repo", "--issue-number"}},
		{"invalidate", nil, []string{"--task-id", "--earliest-gate", "--reason", "--actor"}},
		{"reenter", nil, []string{"--task-id", "--earliest-gate", "--reason", "--actor"}},
	} {
		t.Run(probe.command, func(t *testing.T) {
			code, output := runCLI(t, append([]string{probe.command}, probe.args...)...)
			if code != 2 {
				t.Errorf("exit %d, not the usage code 2:\n%s", code, output)
			}
			for _, name := range probe.missing {
				if !strings.Contains(output, name) {
					t.Errorf("does not name %s as missing:\n%s", name, output)
				}
			}
		})
	}
}

func TestAValueOutsideAChoiceSetIsAUsageErrorRatherThanAnAttempt(t *testing.T) {
	// Refused by the parser, not by the code that would have used it. The
	// difference shows up as an exit code a script can branch on, and as the
	// command not having done half its work first.
	for _, probe := range []struct {
		name  string
		args  []string
		names string
	}{
		{"a gate that is not a gate",
			[]string{"decide", "--task-id", "T", "--gate", "G99", "--role", "product_owner",
				"--decision", "approved", "--actor-id", "a", "--evidence-uri", "u"}, "--gate"},
		{"a role that is not an authority",
			[]string{"decide", "--task-id", "T", "--gate", "G1", "--role", "chief",
				"--decision", "approved", "--actor-id", "a", "--evidence-uri", "u"}, "--role"},
		{"a decision nobody can make",
			[]string{"decide", "--task-id", "T", "--gate", "G1", "--role", "product_owner",
				"--decision", "maybe", "--actor-id", "a", "--evidence-uri", "u"}, "--decision"},
		{"a forge this kernel does not speak to",
			[]string{"publish-gate-status", "--task-id", "T", "--forge", "bitbucket",
				"--as-bot", "b"}, "--forge"},
		{"a link type GitLab has no name for",
			[]string{"create-gate-issues", "--task-id", "T", "--project-path", "a/b",
				"--as-bot", "b", "--link-type", "supersedes"}, "--link-type"},
		{"a runner nothing generates wrappers for",
			[]string{"init", "--runner", "emacs"}, "--runner"},
		{"a gate that is not a gate, on a surgery command",
			[]string{"invalidate", "--task-id", "T", "--earliest-gate", "G0",
				"--reason", "r", "--actor", "a"}, "--earliest-gate"},
	} {
		t.Run(probe.name, func(t *testing.T) {
			code, output := runCLI(t, probe.args...)
			if code != 2 {
				t.Errorf("exit %d, not the usage code 2:\n%s", code, output)
			}
			if !strings.Contains(output, "invalid choice") || !strings.Contains(output, probe.names) {
				t.Errorf("does not report an invalid choice for %s:\n%s", probe.names, output)
			}
		})
	}
}

func TestANumericArgumentThatIsNotANumberIsAUsageError(t *testing.T) {
	for _, probe := range []struct {
		name string
		args []string
		flag string
	}{
		{"a pull request", []string{"publish-reviewer-nudge", "--task-id", "T",
			"--repo", "a/b", "--pr", "first", "--as-bot", "b"}, "--pr"},
		{"a merge request", []string{"request-gate-reviewers-gitlab", "--task-id", "T",
			"--project-path", "a/b", "--mr-iid", "latest", "--as-bot", "b"}, "--mr-iid"},
		{"a review id", []string{"approve-from-github", "--task-id", "T", "--gate", "G1",
			"--role", "product_owner", "--repo", "a/b", "--pr", "1", "--review-id", "newest",
			"--reviewer-login", "x", "--commit-sha", "s"}, "--review-id"},
		{"an issue number", []string{"link-intent-from-github-issue", "--task-id", "T",
			"--role", "product_owner", "--repo", "a/b", "--issue-number", "seven"},
			"--issue-number"},
	} {
		t.Run(probe.name, func(t *testing.T) {
			code, output := runCLI(t, probe.args...)
			if code != 2 {
				t.Errorf("exit %d, not the usage code 2:\n%s", code, output)
			}
			if !strings.Contains(output, "invalid int value") || !strings.Contains(output, probe.flag) {
				t.Errorf("does not report a bad integer for %s:\n%s", probe.flag, output)
			}
		})
	}
}

func TestAnUnknownArgumentIsRefusedRatherThanIgnored(t *testing.T) {
	// A flag nobody declared is almost always a typo for one that matters --
	// `--dry-run` mistyped is a run that writes. Silently ignoring it is the
	// one response that turns a typo into a mutation.
	for _, command := range []string{
		"status", "decide", "init", "repair", "upgrade", "create-gate-issues",
		"create-github-gate-issues", "publish-gate-status", "publish-reviewer-nudge",
		"request-gate-reviewers", "request-gate-reviewers-gitlab", "approve-from-github",
		"link-intent-from-gitlab-issue", "invalidate", "list-gate-issues",
	} {
		t.Run(command, func(t *testing.T) {
			code, output := runCLI(t, command, "--not-a-flag", "x")
			if code != 2 {
				t.Errorf("exit %d, not the usage code 2:\n%s", code, output)
			}
			if !strings.Contains(output, "--not-a-flag") {
				t.Errorf("does not name the argument it refused:\n%s", output)
			}
		})
	}
}

func TestUpgradeRequiresExactlyOneOfCheckAndApply(t *testing.T) {
	// Neither is ambiguous about whether the operator meant to write. Both is
	// a contradiction. The command refuses to guess in either direction.
	code, output := runCLI(t, "upgrade", "--root", t.TempDir())
	if code != 2 {
		t.Errorf("neither flag: exit %d, not 2:\n%s", code, output)
	}
	code, output = runCLI(t, "upgrade", "--root", t.TempDir(), "--check", "--apply")
	if code != 2 {
		t.Errorf("both flags: exit %d, not 2:\n%s", code, output)
	}
	if !strings.Contains(output, "not allowed with argument") {
		t.Errorf("does not say the two conflict:\n%s", output)
	}
}

func TestTheModeCommandsNeverWriteWithoutBeingAskedTo(t *testing.T) {
	// --apply is the flag that changes a forge. Its absence, and its explicit
	// spelling --dry-run, must both leave the world alone -- and a run that
	// reaches a forge at all without --apply is already wrong, which is why
	// these are checked with no mock configured: a dry run that tried to talk
	// to GitLab would fail differently.
	root, manifest := decidableProject(t)
	for _, probe := range []struct {
		name string
		args []string
	}{
		{"create-gate-issues, by default", []string{"create-gate-issues",
			"--project-path", "acme/app", "--as-bot", "b"}},
		{"create-gate-issues, said out loud", []string{"create-gate-issues",
			"--project-path", "acme/app", "--as-bot", "b", "--dry-run"}},
		{"create-github-gate-issues, by default", []string{"create-github-gate-issues",
			"--repo", "acme/app", "--as-bot", "b"}},
		{"create-github-gate-issues, said out loud", []string{"create-github-gate-issues",
			"--repo", "acme/app", "--as-bot", "b", "--dry-run"}},
	} {
		t.Run(probe.name, func(t *testing.T) {
			before := overlayFingerprint(t, root)
			args := append([]string{"--provider", manifest}, probe.args...)
			args = append(args, "--root", root, "--task-id", decideTask,
				"--allow-classification", "internal")
			// 0 or 2: this fixture's authorities are bound to one forge, so
			// the other's dry run legitimately reports refusals. Either way it
			// printed a plan and must not have acted on it.
			code, output := runCLI(t, args...)
			if code != 0 && code != 2 {
				t.Fatalf("a dry run failed: exit %d\n%s", code, output)
			}
			var report map[string]any
			if err := json.Unmarshal([]byte(output), &report); err != nil {
				t.Fatalf("the report is not JSON: %v\n%s", err, output)
			}
			if report["mode"] != "dry-run" {
				t.Errorf("mode is %v, not dry-run", report["mode"])
			}
			if after := overlayFingerprint(t, root); after != before {
				t.Errorf("a dry run changed the project")
			}
		})
	}
}

// overlayFingerprint is every file under the overlay with its contents, so a
// test can say "nothing changed" about a directory rather than about one file
// somebody thought to check.
func overlayFingerprint(t *testing.T, root string) string {
	t.Helper()
	var builder strings.Builder
	overlayRoot := filepath.Join(root, Overlay)
	err := filepath.Walk(overlayRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		relative, _ := filepath.Rel(overlayRoot, path)
		builder.WriteString(relative)
		builder.WriteByte(0)
		builder.Write(data)
		builder.WriteByte(0)
		return nil
	})
	if err != nil {
		t.Fatalf("reading the overlay: %v", err)
	}
	return builder.String()
}

// readDirNames is what a directory contains, for asserting it contains nothing.
func readDirNames(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("reading %s: %v", root, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestDetectDescribesARepositoryWithoutChangingIt(t *testing.T) {
	// `detect` is the one command an operator runs before deciding whether to
	// trust this kernel with a project at all, so it has to be safe to run on
	// something it knows nothing about.
	root := t.TempDir()
	code, output := runCLI(t, "detect", "--root", root)
	if code != 0 {
		t.Fatalf("detect failed on an empty directory: exit %d\n%s", code, output)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("detect did not print JSON: %v\n%s", err, output)
	}
	if _, present := report["detected_stacks"]; !present {
		t.Errorf("detect reports no stacks at all:\n%s", output)
	}
	if report["proposed_profile"] == nil {
		t.Errorf("detect proposes no profile:\n%s", output)
	}
	entries := readDirNames(t, root)
	if len(entries) != 0 {
		t.Errorf("detect wrote into the directory it was asked about: %v", entries)
	}
}

func TestStatusReportsAGateStateAndSaysWhichTask(t *testing.T) {
	root, manifest := decidableProject(t)
	code, output := runCLI(t, "--provider", manifest, "status",
		"--root", root, "--task-id", decideTask)
	if code != 0 {
		t.Fatalf("status failed: exit %d\n%s", code, output)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("status did not print JSON: %v\n%s", err, output)
	}
	if report["task_id"] != decideTask {
		t.Errorf("status reports task %v, not %s", report["task_id"], decideTask)
	}
	if _, present := report["gates"]; !present {
		t.Errorf("status reports no gates:\n%s", output)
	}
}

func TestATaskIdThatEscapesItsDirectoryIsRefused(t *testing.T) {
	// A task id becomes a path segment under the overlay. One containing a
	// traversal would let a caller name a run record outside the project, and
	// every command that takes one has to refuse it -- not just the ones
	// somebody remembered.
	root, manifest := decidableProject(t)
	for _, command := range [][]string{
		{"status"},
		{"list-gate-issues"},
		{"list-github-gate-issues"},
		{"list-reviewer-nudge"},
	} {
		t.Run(command[0], func(t *testing.T) {
			args := append([]string{"--provider", manifest}, command...)
			args = append(args, "--root", root, "--task-id", "../../etc")
			code, output := runCLI(t, args...)
			if code == 0 {
				t.Errorf("a traversing task id was accepted:\n%s", output)
			}
		})
	}
}
