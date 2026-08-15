package orchestration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The sandbox the API runner's tools operate inside.
//
// A port of the policy half of roster/orchestration/mcp/api_runner.py. The
// API runner is the dispatch runner that spawns no coding CLI: it drives an
// OpenAI-compatible chat endpoint and executes the tool calls the model asks
// for. That makes this file the boundary between a model's output and the
// filesystem, so every function here answers "may this happen at all",
// separately from "how would it be done".
//
// Split out from the chat loop deliberately. The loop is a protocol; these
// are the refusals, and they are testable without an endpoint.

// Caps, all ported verbatim. Each bounds a different way a tool loop can
// consume a machine or a context window.
const (
	MaxToolIterations   = 24
	MaxToolResultBytes  = 64 * 1024
	MaxWriteBytes       = 1 * 1024 * 1024
	MaxReadBytes        = 2 * 1024 * 1024
	MaxFilesScanned     = 20000
	MaxSearchMatches    = 200
	MaxAPIResponseBytes = 4 * 1024 * 1024
)

// refusedCommands can never be run from a dispatched role, allowlist or not:
// each starts another agent, and a role that can start agents can escape
// every limit placed on it by starting something without them.
//
// Compared case-folded, so `Codex` is refused exactly as `codex` is.
var refusedCommands = map[string]bool{
	"cadre": true, "codex": true, "claude": true, "cline": true, "agentic-sdlc": true,
}

// ToolDenied is a tool call refused by policy -- a path escape, a denied
// command, a write without authorization.
//
// Deliberately not a dispatch denial: it is reported back to the model as a
// tool result so it can correct a mistaken path and continue. A refusal the
// model cannot see is a refusal it will repeat.
type ToolDenied struct{ message string }

func (e *ToolDenied) Error() string { return e.message }

func toolDeniedf(format string, args ...any) error {
	return &ToolDenied{message: fmt.Sprintf(format, args...)}
}

// ResolveWithinProject resolves a model-supplied path and proves it lands
// inside the project.
//
// Resolution happens *before* the containment check, so a symlink pointing
// out of the tree is caught by the check rather than by trusting the literal
// path. `.git` is refused outright at any depth: rewriting history or a hook
// is not editing the project, and a hook is code that runs later, outside
// this loop and outside every limit it applies.
func ResolveWithinProject(projectRoot, raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", toolDeniedf("path must be a non-empty string")
	}
	if strings.ContainsRune(raw, 0) {
		return "", toolDeniedf("path must not contain NUL")
	}

	base, err := filepath.EvalSymlinks(projectRoot)
	if err != nil {
		base = filepath.Clean(projectRoot)
	}
	base, err = filepath.Abs(base)
	if err != nil {
		return "", toolDeniedf("project root is unusable: %s", err)
	}

	candidate := raw
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(base, candidate)
	}
	// EvalSymlinks fails on a path that does not exist yet, which write_file
	// legitimately produces. Fall back to resolving the deepest existing
	// ancestor, so a symlinked *parent* is still resolved before the check.
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		resolved = resolveExistingAncestor(candidate)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", toolDeniedf("path escapes the project root: %q", raw)
	}

	relative, err := filepath.Rel(base, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", toolDeniedf("path escapes the project root: %q", raw)
	}
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == ".git" {
			return "", toolDeniedf("refusing to touch the git directory: %q", raw)
		}
	}
	return resolved, nil
}

// resolveExistingAncestor resolves the deepest existing ancestor of path and
// re-joins the remainder, so a symlinked directory in the middle of a
// not-yet-existing path is still followed before containment is decided.
func resolveExistingAncestor(path string) string {
	remainder := ""
	current := filepath.Clean(path)
	for {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			if remainder == "" {
				return resolved
			}
			return filepath.Join(resolved, remainder)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return filepath.Clean(path)
		}
		remainder = filepath.Join(filepath.Base(current), remainder)
		current = parent
	}
}

// ReadFileCapped reads a regular file, refusing a symlink at the final
// component and anything that is not a regular file.
//
// Open-then-fstat, not stat-then-open: the kernel decides, so a symlink
// swapped in after a check cannot redirect the read.
func ReadFileCapped(path string) (string, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|noFollowFlag, 0)
	if err != nil {
		return "", toolDeniedf("cannot read %s: %s", filepath.Base(path), err)
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return "", toolDeniedf("cannot read %s: %s", filepath.Base(path), err)
	}
	if !info.Mode().IsRegular() {
		return "", toolDeniedf("refusing to read a non-regular file: %s", filepath.Base(path))
	}

	// One byte over the cap, so a file exactly at the limit still reads and
	// anything larger is refused rather than silently truncated.
	buffer := make([]byte, MaxReadBytes+1)
	read, err := file.Read(buffer)
	if err != nil && read == 0 {
		return "", toolDeniedf("cannot read %s: %s", filepath.Base(path), err)
	}
	if read > MaxReadBytes {
		return "", toolDeniedf("%s is larger than the %d byte read limit",
			filepath.Base(path), MaxReadBytes)
	}
	return string(buffer[:read]), nil
}

// WriteFileCapped writes payload, refusing a symlink at the final component.
//
// The read path has always refused symlinks at open time. A write path using
// a plain create *follows* one, which would leave containment resting
// entirely on the resolve taken before the write -- a check-then-open gap a
// symlink appearing in between would defeat, redirecting the write anywhere
// this process can reach. Team dispatch runs members concurrently against one
// project root, so that interleaving is not theoretical.
//
// O_TRUNC rather than O_EXCL: write_file is documented as create-or-
// overwrite, and O_EXCL would refuse every legitimate overwrite.
func WriteFileCapped(path string, payload []byte) error {
	if len(payload) > MaxWriteBytes {
		return toolDeniedf("refusing to write %d bytes; the limit is %d",
			len(payload), MaxWriteBytes)
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|noFollowFlag, 0o644)
	if err != nil {
		// ELOOP here means the final component is a symlink. Reported as a
		// policy refusal, because that is what it is.
		return toolDeniedf("cannot write %s: %s", filepath.Base(path), err)
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return toolDeniedf("cannot write %s: %s", filepath.Base(path), err)
	}
	if !info.Mode().IsRegular() {
		return toolDeniedf("refusing to write a non-regular file: %s", filepath.Base(path))
	}
	if _, err := file.Write(payload); err != nil {
		return toolDeniedf("cannot write %s: %s", filepath.Base(path), err)
	}
	return nil
}

// CheckCommandAllowed decides whether the model may run a command.
//
// Order matters and is not interchangeable: the never-runnable set is checked
// *before* the operator's allowlist, so an operator who allowlists `codex` --
// by choice or by mistake -- still cannot give a dispatched role the ability
// to start another agent. An allowlist is a narrowing instrument; it cannot
// widen past this.
func CheckCommandAllowed(command string, allowlist []string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", toolDeniedf("command must be a non-empty string")
	}
	if refusedCommands[strings.ToLower(command)] {
		return "", toolDeniedf(
			"%q is never runnable from a dispatched role (it would start another agent)", command)
	}

	permitted := false
	for _, entry := range allowlist {
		if entry == command {
			permitted = true
			break
		}
	}
	if !permitted {
		listed := strings.Join(allowlist, ", ")
		if listed == "" {
			listed = "empty"
		}
		return "", toolDeniedf(
			"%q is not in the operator's runners.api_command_allowlist (%s)", command, listed)
	}

	resolved, err := exec.LookPath(command)
	if err != nil {
		return "", toolDeniedf("%q is allowlisted but not found on PATH", command)
	}
	return resolved, nil
}

// AvailableToolNames is the tool set a role may use.
//
// Write tools appear only when the role is authorized to write, and the
// command tool only when the operator has configured a non-empty allowlist --
// an unconfigured allowlist means the capability is unavailable, not that
// everything is permitted.
func AvailableToolNames(writesAllowed bool, commandAllowlist []string) []string {
	names := []string{"read_file", "list_files", "search"}
	if writesAllowed {
		names = append(names, "write_file", "edit_file")
	}
	if len(commandAllowlist) > 0 {
		names = append(names, "run_command")
	}
	return names
}

// TruncateToolResult bounds what a tool hands back to the model.
//
// A tool that returns a whole repository would blow the context window and
// bury the answer. The marker is deliberately visible: a silently truncated
// result reads as a complete one.
func TruncateToolResult(text string) string {
	if len(text) <= MaxToolResultBytes {
		return text
	}
	return text[:MaxToolResultBytes] + "\n... [truncated at " +
		fmt.Sprintf("%d", MaxToolResultBytes) + " bytes]"
}
