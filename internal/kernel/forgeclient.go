package kernel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Talking to a forge, by shelling out to its own CLI.
//
// Neither client here speaks HTTP. They run `gh api` and `glab api`, which
// means credentials never pass through this process: the operator has already
// configured the forge CLI, and this kernel inherits whatever identity that
// CLI is authenticated as. A kernel that read a token from the environment
// would be one more place a token can leak from, and one more thing to get
// wrong on rotation.
//
// Three properties every call here shares, each for a reason:
//
//   - A bounded timeout. A forge that stops answering must not hang a
//     publication holding a ledger lock, because that lock is never broken
//     automatically.
//   - An empty working directory. `gh` and `glab` both read repository
//     context from the current directory, and inheriting it would let the
//     directory a command happened to be run from change which repository is
//     addressed.
//   - Request bodies through a file, never argv. Anything in argv is visible
//     in the process table to every user on the machine, and these bodies
//     carry a project's own text.
//
// And a limitation worth stating rather than hiding: both CLIs report HTTP
// status as prose on stderr, not as a structured field. Detecting a 404 means
// looking for it in that prose. That is what the upstream tools expose.

// forgeTimeout bounds a single forge call.
const forgeTimeout = 30 * time.Second

// Mock environment variables. Each names a JSON file holding canned responses
// for one forge, and they are deliberately separate: a single file
// multiplexing both forges would let a GitHub test pass on a GitLab fixture.
//
// The names are the Python kernel's, exactly. Every fixture written against
// that kernel names one of these, so a tidier name here would silently stop
// those fixtures being read while the tests using them still passed -- the
// mock would simply be absent, and absent means "use the network".
const (
	GitHubReadMockEnv   = "AGENTIC_SDLC_TEST_GITHUB_READ_FILE"
	GitLabIssueMockEnv  = "AGENTIC_SDLC_TEST_ISSUE_CREATE_FILE"
	GitHubIssueMockEnv  = "AGENTIC_SDLC_TEST_GITHUB_ISSUE_FILE"
	GitHubStatusMockEnv = "AGENTIC_SDLC_TEST_GITHUB_WRITE_FILE"
)

// forgeResult is one completed forge invocation.
type forgeResult struct {
	exitCode int
	stdout   []byte
	stderr   string
}

// ok reports whether the call succeeded.
func (r forgeResult) ok() bool { return r.exitCode == 0 }

// detail is the stderr text an error message quotes, or a stand-in.
func (r forgeResult) detail(tool string) string {
	trimmed := strings.TrimSpace(r.stderr)
	if trimmed == "" {
		return "unknown " + tool + " api failure"
	}
	return trimmed
}

// runForgeCLI invokes a forge CLI and returns what it said.
//
// bodyPath, when set, is passed as `--input` and is written by the caller into
// the same temporary directory, so it disappears with it.
func runForgeCLI(argv []string, workingDirectory string) (forgeResult, error) {
	timeout, cancel := context.WithTimeout(context.Background(), forgeTimeout)
	defer cancel()

	command := exec.CommandContext(timeout, argv[0], argv[1:]...)
	command.Dir = workingDirectory
	stdout, err := command.Output()

	var exit *exec.ExitError
	switch {
	case err == nil:
		return forgeResult{exitCode: 0, stdout: stdout}, nil
	case errors.Is(timeout.Err(), context.DeadlineExceeded):
		return forgeResult{}, forgeLaunchError(argv, "timed out")
	case errors.As(err, &exit):
		return forgeResult{
			exitCode: exit.ExitCode(), stdout: stdout, stderr: string(exit.Stderr),
		}, nil
	default:
		return forgeResult{}, forgeLaunchError(argv, "failed to start")
	}
}

// runForgeCLIIn creates a temporary working directory, runs, and cleans up.
func runForgeCLIIn(tool string, argv []string) (forgeResult, error) {
	directory, err := os.MkdirTemp("", "agentic-sdlc-"+tool+"-")
	if err != nil {
		return forgeResult{}, err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	return runForgeCLI(argv, directory)
}

// runForgeCLIWithBody writes a request body to a 0600 file in a temporary
// directory and passes its path, rather than putting the body in argv.
func runForgeCLIWithBody(
	tool string, argv []string, body any,
) (forgeResult, error) {
	directory, err := os.MkdirTemp("", "agentic-sdlc-"+tool+"-")
	if err != nil {
		return forgeResult{}, err
	}
	defer func() { _ = os.RemoveAll(directory) }()

	encoded, err := json.Marshal(body)
	if err != nil {
		return forgeResult{}, err
	}
	bodyPath := filepath.Join(directory, "request-body.json")
	// 0600 before anything is in it: the body carries a project's own text,
	// and the window between create and chmod is a window where it is
	// world-readable.
	if err := os.WriteFile(bodyPath, encoded, 0o600); err != nil {
		return forgeResult{}, err
	}
	return runForgeCLI(append(argv, "--input", bodyPath), directory)
}

// forgeLaunchError explains a CLI that did not run, rather than one that ran
// and failed.
//
// The distinction matters to whoever reads it: a missing `gh` is an
// installation problem, and a `gh` that returned 403 is a permissions one.
func forgeLaunchError(argv []string, verb string) error {
	command := "the forge CLI"
	switch {
	case len(argv) >= 2:
		command = argv[0] + " " + argv[1]
	case len(argv) == 1:
		command = argv[0]
	}
	tool := "the tool"
	if len(argv) > 0 {
		tool = argv[0]
	}
	return fmt.Errorf("`%s` %s -- is %s installed and reachable?", command, verb, tool)
}

// parseForgeJSON decodes a successful response.
func parseForgeJSON(tool, context string, stdout []byte) (any, error) {
	var value any
	if err := json.Unmarshal(stdout, &value); err != nil {
		// Wrapped, so a caller can still reach the decoder's own error, but
		// worded to match the Python kernel's message -- an operator greps
		// this to find out which call came back with something unparseable.
		return nil, fmt.Errorf("%s: %s exited 0 but stdout was not valid JSON (%w)",
			context, tool, err)
	}
	return value, nil
}

// parseForgeJSONObject decodes a response that must be an object.
func parseForgeJSONObject(tool, context, description string, stdout []byte) (map[string]any, error) {
	value, err := parseForgeJSON(tool, context, stdout)
	if err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a JSON object", description)
	}
	return object, nil
}

// isNotFoundError reports whether a CLI's stderr describes a 404.
//
// Substring matching on prose, because that is what `gh` and `glab` expose --
// neither surfaces the HTTP status as a structured field. Documented rather
// than worked around: the alternative is parsing their human-readable output
// more aggressively, which breaks on a wording change instead of a format one.
func isNotFoundError(stderr string) bool {
	return strings.Contains(stderr, "404") || strings.Contains(stderr, "Not Found")
}

// loadForgeMock reads the canned responses for one forge, if any are
// configured.
//
// Returns nil when the variable is unset, which is the ordinary case: the mock
// exists so tests and `--i-know-this-is-mocked` runs can exercise these paths
// without a network, and nothing silently falls back to it.
func loadForgeMock(variable string) (map[string]any, error) {
	path := os.Getenv(variable)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	object, ok := payload.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must contain a JSON object", variable)
	}
	return object, nil
}

// percentEncode escapes a path segment for a forge API URL.
//
// Every segment, including ones that look safe: a repository name is
// attacker-influenced in the sense that it comes from a project's own config,
// and an unescaped "../" in one addresses a different API endpoint.
func percentEncode(value string) string {
	var builder strings.Builder
	for _, b := range []byte(value) {
		if isUnreservedURLByte(b) {
			builder.WriteByte(b)
			continue
		}
		fmt.Fprintf(&builder, "%%%02X", b)
	}
	return builder.String()
}

// isUnreservedURLByte matches Python's quote(safe="") set: letters, digits,
// and the four unreserved punctuation characters.
func isUnreservedURLByte(b byte) bool {
	switch {
	case b >= 'A' && b <= 'Z', b >= 'a' && b <= 'z', b >= '0' && b <= '9':
		return true
	case b == '-', b == '_', b == '.', b == '~':
		return true
	default:
		return false
	}
}

// encodeRepoPath escapes an "owner/name" pair, keeping the separator.
func encodeRepoPath(repo string) string {
	owner, name, found := strings.Cut(repo, "/")
	if !found {
		return percentEncode(repo)
	}
	return percentEncode(owner) + "/" + percentEncode(name)
}
