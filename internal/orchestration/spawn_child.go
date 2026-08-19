package orchestration

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"time"
)

// Spawning a dispatched child: the prompt on stdin, its own process group, a
// wall-clock deadline that kills the group, and a cap on what comes back.
//
// A port of dispatch_core.py's spawn_and_wait. The Go dispatch path had
// SpawnAndWait, which ran exec.Command(...).CombinedOutput() with no stdin,
// no deadline, no process group and no output cap -- and both runner
// spawners passed the prompt as an argv element instead. It has since been
// deleted: nothing called it, nothing tested it, and it accepted a timeout
// parameter it never read, so anyone who found it and passed one would have
// got no deadline at all.
//
// The prompt goes on stdin for two reasons that are not stylistic. It
// contains the caller's brief, which is untrusted and can be long: as an
// argv element it lands in the process table, readable by any user on the
// machine via ps, and it counts against ARG_MAX, so a large brief fails at
// the exec with an error that names none of that.

// spawnResult is the shape both runner spawners return.
type spawnChildOptions struct {
	Argv       []string
	Prompt     string
	Env        map[string]string
	WorkingDir string
	Timeout    time.Duration
}

// spawnChildWithPrompt runs a child to completion and returns the same result
// map shape the rest of the dispatch path consumes.
//
// Output is capped rather than streamed to the caller: what comes back is
// untrusted text that a model will read, and an unbounded child can produce
// more of it than the parent can hold. Truncation is reported in the result
// so a caller cannot mistake a cut-off transcript for a complete one.
func spawnChildWithPrompt(options spawnChildOptions) map[string]any {
	if len(options.Argv) == 0 {
		return map[string]any{"status": "error", "reason": "no command to spawn"}
	}

	command := exec.Command(options.Argv[0], options.Argv[1:]...) // #nosec G204 -- argv is built from validated role fields, never caller text
	command.Env = environmentMapToSlice(options.Env)
	if options.WorkingDir != "" {
		// Pinned to the dispatch's own project root. For an MCP server this
		// process's cwd is wherever the host CLI was launched, which has no
		// relation to the project being dispatched.
		command.Dir = options.WorkingDir
	}
	command.Stdin = bytes.NewReader([]byte(options.Prompt))

	var captured bytes.Buffer
	// One byte past the cap, so truncation is detectable rather than assumed.
	writer := &cappedWriter{limit: MaxChildOutputBytes + 1, buffer: &captured}
	command.Stdout = writer
	command.Stderr = writer

	setProcessGroup(command)

	started := time.Now()
	if err := command.Start(); err != nil {
		return map[string]any{
			"status": "unavailable",
			"reason": "failed to spawn child process " + options.Argv[0] + ": " + err.Error(),
		}
	}

	timeout := options.Timeout
	if timeout <= 0 {
		timeout = time.Duration(DefaultTimeoutSeconds) * time.Second
	}
	finished := make(chan error, 1)
	go func() { finished <- command.Wait() }()

	var waitErr error
	timedOut := false
	select {
	case waitErr = <-finished:
	case <-time.After(timeout):
		timedOut = true
		killProcessGroup(command.Process.Pid)
		_ = command.Process.Kill()
		// Reaped so the child does not linger as a zombie, and so the
		// output written before the kill is still returned.
		waitErr = <-finished
	}

	output := captured.String()
	truncated := len(output) > MaxChildOutputBytes
	if truncated {
		output = output[:MaxChildOutputBytes]
	}

	exitCode := 0
	var exitErr *exec.ExitError
	switch {
	case timedOut:
		exitCode = -1
	case errors.As(waitErr, &exitErr):
		exitCode = exitErr.ExitCode()
	case waitErr != nil:
		exitCode = -1
	}

	status := "completed"
	if timedOut || exitCode != 0 {
		status = "failed"
	}

	result := map[string]any{
		"status":           status,
		"exit_code":        exitCode,
		"timed_out":        timedOut,
		"duration_seconds": time.Since(started).Seconds(),
		"pid":              command.Process.Pid,
		// The child's stdout returns to the parent model as this call's
		// result, so it is fenced as untrusted before it is handed over.
		"output": WrapUntrustedOutput(output),
	}
	if truncated {
		result["output_truncated"] = true
	}
	return result
}

// cappedWriter stops accumulating once the limit is reached, so a child that
// writes without bound cannot exhaust this process's memory. Writes past the
// limit are discarded and still reported as accepted -- refusing them would
// make the child fail on a broken pipe rather than on its own terms.
type cappedWriter struct {
	limit  int
	buffer *bytes.Buffer
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	remaining := w.limit - w.buffer.Len()
	if remaining <= 0 {
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = w.buffer.Write(p[:remaining])
		return len(p), nil
	}
	return w.buffer.Write(p)
}

var _ io.Writer = (*cappedWriter)(nil)
