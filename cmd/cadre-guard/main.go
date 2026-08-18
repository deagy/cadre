// Command cadre-guard is the PreToolUse hook that refuses destructive git
// invocations.
//
// A separate binary rather than a `cadre` subcommand, deliberately. This one
// ships in the plugin per platform, so its size is the whole argument: the
// full CLI carries the knowledge store and its cgo SQLite driver and is an
// order of magnitude larger. Nothing here imports anything the decision does
// not need.
//
// It reads a PreToolUse payload on stdin and writes a deny decision on stdout,
// exiting 0 either way. Silence means no opinion, which lets any other
// configured hook or the normal permission flow decide.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/deagy/cadre/cli/internal/guard"
)

// disableEnvVar switches the guard off entirely. Checked first, before stdin
// is read and before any parsing, so a wedged guard can always be stepped past.
const disableEnvVar = "CADRE_DISABLE_WORKSPACE_MUTATION_GUARD"

type hookPayload struct {
	HookEventName string `json:"hook_event_name"`
	ToolName      string `json:"tool_name"`
	ToolInput     struct {
		Command string `json:"command"`
	} `json:"tool_input"`
	Cwd string `json:"cwd"`
}

func main() {
	os.Exit(run())
}

func run() int {
	if disabled := strings.ToLower(strings.TrimSpace(os.Getenv(disableEnvVar))); disabled == "1" || disabled == "true" {
		return 0
	}

	var payload hookPayload
	if err := json.NewDecoder(os.Stdin).Decode(&payload); err != nil {
		// Malformed input: fail open. A guard that crashes or hangs on bad
		// input is a guard that gets disabled outright.
		return 0
	}
	if payload.HookEventName != "PreToolUse" || payload.ToolName != "Bash" {
		return 0
	}
	if strings.TrimSpace(payload.ToolInput.Command) == "" {
		return 0
	}
	cwd := payload.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	decision := evaluate(payload.ToolInput.Command, cwd)
	if decision == nil {
		return 0
	}
	encoded, err := json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]string{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "deny",
			"permissionDecisionReason": decision.Reason,
		},
	})
	if err != nil {
		return 0
	}
	fmt.Println(string(encoded))
	return 0
}

// evaluate isolates the decision so an unexpected panic inside it allows the
// command rather than failing the hook, matching the Python's catch-all.
func evaluate(command, cwd string) (decision *guard.Decision) {
	defer func() {
		if recovered := recover(); recovered != nil {
			fmt.Fprintf(os.Stderr, "cadre-guard: internal error, allowing: %v\n", recovered)
			decision = nil
		}
	}()
	return guard.EvaluateCommand(command, cwd)
}
