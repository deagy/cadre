package kernel

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// The sidecar ledgers, and the read-only half of the subcommands that use them.
//
// Four kinds of forge activity leave a ledger next to the run record: gate
// issues on GitLab and on GitHub, the one-way gate-status comment, and the
// reviewer nudge. Each `list-*` subcommand prints one back and touches nothing
// -- no network, no lock, no write.
//
// Two properties are load-bearing and easy to lose:
//
//   - A missing ledger is an empty ledger, not an error. A task nobody has
//     published issues for has no file, and that is a fact about the task
//     rather than a fault -- so the reader answers with the same skeleton the
//     writer would start from.
//   - The filename is forge-qualified. A task tracked on both GitLab and
//     GitHub keeps two ledgers; a shared filename would let one forge's
//     publication silently clobber the other's record of what it created.

// safeTaskIDPattern is the whole vocabulary a task id may use.
//
// A task id becomes a directory name, so anything else is either unportable
// (a colon on Windows), lossy (a trailing space), or an escape (a slash, a
// "..").
var safeTaskIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// SafeTaskID validates a task id before it is used as a path component.
func SafeTaskID(value string) (string, error) {
	if !safeTaskIDPattern.MatchString(value) || value == "." || value == ".." {
		return "", fmt.Errorf(
			"task ID must already be a portable, non-lossy ID using only letters, " +
				"numbers, dot, underscore, or hyphen")
	}
	return value, nil
}

// ledgerSchemaVersion is shared by every sidecar below; they were introduced
// together and have moved together since.
const ledgerSchemaVersion = 1

// gateStatusForges is the order `list-gate-status` reports in. It has no
// --forge flag: a task may have been published to either or both over its
// life, and reporting only one would answer a question nobody asked.
//
// The forge names themselves are declared in gatestatus.go, next to the
// command that decides which one to publish to.
var gateStatusForges = []string{ForgeGitHub, ForgeGitLab}

// readLedger returns a task's sidecar ledger, or the supplied empty skeleton
// when there is no file.
func readLedger(root, taskID, filename string, empty func() any) (any, error) {
	path, err := ConfinedPath(root, Overlay, "runs", taskID, filename)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return empty(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Decoded ordered and re-rendered rather than copied: the Python kernel
	// prints the parsed value, so a ledger written by hand with different
	// spacing must come back normalised the same way.
	return DecodeOrdered(data)
}

// orderedLedger builds a skeleton whose keys are in the order the Python
// kernel writes them. The order is not decorative -- it is what a caller
// diffing two kernels' output sees first.
func orderedLedger(pairs ...any) *orderedObject {
	object := &orderedObject{values: map[string]any{}}
	for index := 0; index+1 < len(pairs); index += 2 {
		key, _ := pairs[index].(string)
		object.set(key, pairs[index+1])
	}
	return object
}

// ReadGateIssuesLedger reports the GitLab gate-issues ledger for a task.
func ReadGateIssuesLedger(root, taskID string) (any, error) {
	return readLedger(root, taskID, "gate-issues-"+ForgeGitLab+".json", func() any {
		return orderedLedger(
			"schema_version", ledgerSchemaVersion,
			"task_id", taskID,
			"project_path", nil,
			"bot_username", nil,
			"mocked", false,
			"entries", &orderedObject{values: map[string]any{}},
		)
	})
}

// ReadGitHubGateIssuesLedger reports the GitHub gate-issues ledger.
func ReadGitHubGateIssuesLedger(root, taskID string) (any, error) {
	return readLedger(root, taskID, "gate-issues-"+ForgeGitHub+".json", func() any {
		return orderedLedger(
			"schema_version", ledgerSchemaVersion,
			"task_id", taskID,
			"repo", nil,
			"bot_login", nil,
			"mocked", false,
			"entries", &orderedObject{values: map[string]any{}},
		)
	})
}

// ReadGateStatusLedgers reports both forges' gate-status ledgers.
func ReadGateStatusLedgers(root, taskID string) (any, error) {
	result := &orderedObject{values: map[string]any{}}
	for _, forge := range gateStatusForges {
		ledger, err := readLedger(root, taskID, "gate-status-"+forge+".json",
			func() any { return emptyCommentLedger(taskID, forge) })
		if err != nil {
			return nil, err
		}
		result.set(forge, ledger)
	}
	return result, nil
}

// ReadReviewerNudgeLedger reports the GitHub reviewer-nudge ledger.
func ReadReviewerNudgeLedger(root, taskID string) (any, error) {
	return readLedger(root, taskID, "reviewer-nudge-"+ForgeGitHub+".json",
		func() any { return emptyCommentLedger(taskID, ForgeGitHub) })
}

// emptyCommentLedger is the skeleton the two comment-publishing sidecars
// share: both render one marked comment onto one target and remember what
// they rendered.
func emptyCommentLedger(taskID, forge string) *orderedObject {
	return orderedLedger(
		"schema_version", ledgerSchemaVersion,
		"task_id", taskID,
		"forge", forge,
		"target", nil,
		"bot_username", nil,
		"mocked", false,
		"marker", nil,
		"entries", []any{},
	)
}

// listLedgerCmd is the shared front end for the four `list-*` subcommands.
//
// They differ only in which file they read, so the argument handling, the
// task-id validation and the output shape live here once.
func listLedgerCmd(
	name string, args []string, stdout, stderr io.Writer,
	read func(root, taskID string) (any, error),
) int {
	root, taskID := ".", ""
	for index := 0; index < len(args); index++ {
		switch {
		case args[index] == "--root" && index+1 < len(args):
			index++
			root = args[index]
		case strings.HasPrefix(args[index], "--root="):
			root = strings.TrimPrefix(args[index], "--root=")
		case args[index] == "--task-id" && index+1 < len(args):
			index++
			taskID = args[index]
		case strings.HasPrefix(args[index], "--task-id="):
			taskID = strings.TrimPrefix(args[index], "--task-id=")
		default:
			_, _ = fmt.Fprintf(stderr, "usage: agentic-sdlc %s [--root ROOT] --task-id TASK_ID\n", name)
			return 2
		}
	}
	if taskID == "" {
		_, _ = fmt.Fprintf(stderr,
			"agentic-sdlc %s: error: the following arguments are required: --task-id\n", name)
		return 2
	}

	resolved, err := resolveExisting(root)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", jsonError(err))
		return 1
	}
	safeID, err := SafeTaskID(taskID)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", jsonError(err))
		return 1
	}
	ledger, err := read(resolved, safeID)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s\n", jsonError(err))
		return 1
	}
	_, _ = fmt.Fprint(stdout, RenderIndented(ledger))
	return 0
}
