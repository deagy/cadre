package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	cadreconfig "github.com/deagy/cadre/cli/internal/config"
)

// The API runner's tool-call loop: the part that joins the endpoint
// (api_runner_endpoint.go) to the sandbox (api_runner_sandbox.go).
//
// A port of api_runner.py's run_api_dispatch. The loop itself is
// unremarkable -- ask, execute what comes back, ask again -- and everything
// interesting is in when it stops and what it reports when it does.

// ToolCall is one function call the model asked for.
type ToolCall struct {
	ID        string
	Name      string
	Arguments map[string]any
}

// Toolbox executes tool calls inside the sandbox and records what was
// actually done.
//
// The accounting is not telemetry: it decides how an endpoint failure is
// classified, because a dispatch that wrote files before failing is a
// different event from one that never started.
type Toolbox struct {
	ProjectRoot      string
	WritesAllowed    bool
	CommandAllowlist []string
	Deadline         time.Time

	FilesWritten []string
	CommandsRun  []string
	ToolCalls    int
}

// Mutated reports whether anything on disk or in the environment changed.
func (box *Toolbox) Mutated() bool {
	return len(box.FilesWritten) > 0 || len(box.CommandsRun) > 0
}

// Execute runs one tool call and returns the text handed back to the model.
//
// A refusal is returned as a *result*, not an error: the model can correct a
// mistaken path and continue, and a refusal it cannot see is one it will
// repeat. Only conditions that end the dispatch are errors.
func (box *Toolbox) Execute(call ToolCall) string {
	box.ToolCalls++
	result, err := box.execute(call)
	if err != nil {
		return TruncateToolResult("error: " + err.Error())
	}
	return TruncateToolResult(result)
}

func (box *Toolbox) execute(call ToolCall) (string, error) {
	stringArg := func(name string) (string, error) {
		value, _ := call.Arguments[name].(string)
		if strings.TrimSpace(value) == "" {
			return "", toolDeniedf("%s requires a non-empty %q", call.Name, name)
		}
		return value, nil
	}

	switch call.Name {
	case "read_file":
		raw, err := stringArg("path")
		if err != nil {
			return "", err
		}
		path, err := ResolveWithinProject(box.ProjectRoot, raw)
		if err != nil {
			return "", err
		}
		return ReadFileCapped(path)

	case "list_files":
		pattern, _ := call.Arguments["pattern"].(string)
		return box.listFiles(pattern)

	case "search":
		needle, err := stringArg("query")
		if err != nil {
			return "", err
		}
		return box.search(needle)

	case "write_file", "edit_file":
		if !box.WritesAllowed {
			return "", toolDeniedf(
				"%s is not available: this dispatch is not authorized to write", call.Name)
		}
		raw, err := stringArg("path")
		if err != nil {
			return "", err
		}
		content, _ := call.Arguments["content"].(string)
		path, err := ResolveWithinProject(box.ProjectRoot, raw)
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", toolDeniedf("cannot create the parent directory: %s", err)
		}
		if err := WriteFileCapped(path, []byte(content)); err != nil {
			return "", err
		}
		// Recorded only after the write succeeded. A refused write is not a
		// mutation, and counting it would misclassify a later endpoint
		// failure as having changed the workspace.
		relative, relErr := filepath.Rel(box.ProjectRoot, path)
		if relErr != nil {
			relative = path
		}
		box.FilesWritten = append(box.FilesWritten, relative)
		return fmt.Sprintf("wrote %d bytes to %s", len(content), relative), nil

	case "run_command":
		return box.runCommand(call)
	}
	return "", toolDeniedf("%q is not an available tool", call.Name)
}

func (box *Toolbox) listFiles(pattern string) (string, error) {
	if pattern == "" {
		pattern = "*"
	}
	var found []string
	scanned := 0
	err := filepath.WalkDir(box.ProjectRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		scanned++
		if scanned > MaxFilesScanned {
			return filepath.SkipAll
		}
		relative, relErr := filepath.Rel(box.ProjectRoot, path)
		if relErr != nil {
			return nil
		}
		if matched, _ := filepath.Match(pattern, filepath.Base(path)); matched || pattern == "*" {
			found = append(found, relative)
		}
		return nil
	})
	if err != nil {
		return "", toolDeniedf("cannot list files: %s", err)
	}
	sort.Strings(found)
	return strings.Join(found, "\n"), nil
}

func (box *Toolbox) search(needle string) (string, error) {
	var matches []string
	scanned := 0
	err := filepath.WalkDir(box.ProjectRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil || len(matches) >= MaxSearchMatches {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		scanned++
		if scanned > MaxFilesScanned {
			return filepath.SkipAll
		}
		contents, readErr := ReadFileCapped(path)
		if readErr != nil {
			return nil
		}
		relative, _ := filepath.Rel(box.ProjectRoot, path)
		for number, line := range strings.Split(contents, "\n") {
			if strings.Contains(line, needle) {
				matches = append(matches, fmt.Sprintf("%s:%d: %s", relative, number+1, strings.TrimSpace(line)))
				if len(matches) >= MaxSearchMatches {
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		return "", toolDeniedf("cannot search: %s", err)
	}
	return strings.Join(matches, "\n"), nil
}

func (box *Toolbox) runCommand(call ToolCall) (string, error) {
	name, _ := call.Arguments["command"].(string)
	resolved, err := CheckCommandAllowed(name, box.CommandAllowlist)
	if err != nil {
		return "", err
	}

	var arguments []string
	if raw, ok := call.Arguments["args"].([]any); ok {
		for _, item := range raw {
			text, ok := item.(string)
			if !ok {
				return "", toolDeniedf("args must be a list of strings")
			}
			arguments = append(arguments, text)
		}
	}

	remaining := time.Until(box.Deadline)
	if remaining <= 0 {
		return "", toolDeniedf("the dispatch deadline has passed; not starting a new command")
	}
	ctx, cancel := context.WithTimeout(context.Background(), remaining)
	defer cancel()

	command := exec.CommandContext(ctx, resolved, arguments...)
	command.Dir = box.ProjectRoot
	output, runErr := command.CombinedOutput()

	// Recorded whether or not it exited zero: it ran, and that is what the
	// accounting is about.
	box.CommandsRun = append(box.CommandsRun, name)
	if runErr != nil {
		return fmt.Sprintf("%s exited non-zero: %s\n%s", name, runErr, output), nil
	}
	return string(output), nil
}

// APIDispatchResult is what the loop hands back to dispatch_core.
type APIDispatchResult struct {
	ExitCode     int
	Transcript   string
	FilesWritten []string
	CommandsRun  []string
	ToolCalls    int
	Completed    bool
	TimedOut     bool
}

// ParseToolCalls reads the tool calls out of one assistant message.
//
// Arguments arrive as a JSON *string*, not an object -- that is the wire
// format, and decoding it here means a malformed argument blob is one
// refused tool call rather than a failed dispatch.
func ParseToolCalls(message map[string]any) []ToolCall {
	raw, ok := message["tool_calls"].([]any)
	if !ok {
		return nil
	}
	var calls []ToolCall
	for _, entry := range raw {
		object, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		function, ok := object["function"].(map[string]any)
		if !ok {
			continue
		}
		name, _ := function["name"].(string)
		if name == "" {
			continue
		}
		identifier, _ := object["id"].(string)

		arguments := map[string]any{}
		switch encoded := function["arguments"].(type) {
		case string:
			if strings.TrimSpace(encoded) != "" {
				_ = json.Unmarshal([]byte(encoded), &arguments)
			}
		case map[string]any:
			arguments = encoded
		}
		calls = append(calls, ToolCall{ID: identifier, Name: name, Arguments: arguments})
	}
	return calls
}

// RunAPIDispatch drives the endpoint until the model stops asking for tools,
// the deadline passes, or the iteration cap is reached.
func RunAPIDispatch(
	ctx context.Context,
	endpoint *ChatEndpoint,
	box *Toolbox,
	messages []ChatMessage,
	tools []map[string]any,
	timeout time.Duration,
) (*APIDispatchResult, error) {
	deadline := time.Now().Add(timeout)
	box.Deadline = deadline

	var transcript []string
	result := &APIDispatchResult{}

	iterations := 0
	for ; iterations < MaxToolIterations; iterations++ {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			result.TimedOut = true
			break
		}

		// Bounded by what is left of the caller's deadline, not just the
		// endpoint's own ceiling -- otherwise one slow response overruns a
		// budget the rest of the pipeline treats as real.
		message, err := endpoint.Complete(ctx, messages, tools, remaining)
		if err != nil {
			if !box.Mutated() {
				// Nothing was mutated, so there is no accounting to preserve
				// and the honest classification is the original one: an
				// infrastructure failure. Returned unchanged so a total
				// endpoint outage does not start masquerading as a dispatch
				// that ran and failed.
				return nil, err
			}
			// Past this point the workspace HAS been mutated. Letting the
			// error escape would discard that accounting and leave the audit
			// trail reporting an "unavailable" dispatch that in fact wrote
			// files -- the one outcome an auditor most needs to see. Fall
			// through to normal result assembly instead.
			transcript = append(transcript,
				fmt.Sprintf("\n[dispatch stopped: endpoint failure: %s]", err))
			transcript = append(transcript,
				"[the workspace was already modified before this failure; "+
					"files_written and commands_run below are real]")
			result.ExitCode = 1
			break
		}

		assistant := assistantMessage(message)
		if content := strings.TrimSpace(assistant.Content); content != "" {
			transcript = append(transcript, content)
		}

		calls := ParseToolCalls(assistantRaw(message))
		if len(calls) == 0 {
			result.Completed = true
			break
		}

		// Echoed back so the endpoint sees a well-formed assistant turn
		// preceding the tool results. Without it, some endpoints reject the
		// tool messages as unsolicited.
		messages = append(messages, assistant)
		for _, call := range calls {
			messages = append(messages, ChatMessage{
				Role:       "tool",
				ToolCallID: call.ID,
				Content:    box.Execute(call),
			})
		}
	}

	if iterations >= MaxToolIterations {
		transcript = append(transcript,
			fmt.Sprintf("\n[dispatch stopped after %d tool iterations]", MaxToolIterations))
	}
	if result.TimedOut {
		transcript = append(transcript,
			fmt.Sprintf("\n[dispatch stopped at the %.0fs deadline]", timeout.Seconds()))
	}

	result.Transcript = strings.Join(transcript, "\n")
	result.FilesWritten = box.FilesWritten
	result.CommandsRun = box.CommandsRun
	result.ToolCalls = box.ToolCalls
	return result, nil
}

// assistantRaw pulls the assistant message out of a completion response.
func assistantRaw(response map[string]any) map[string]any {
	choices, ok := response["choices"].([]any)
	if !ok || len(choices) == 0 {
		return map[string]any{}
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		return map[string]any{}
	}
	message, ok := choice["message"].(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return message
}

// assistantMessage rebuilds the assistant turn to echo back, carrying only
// the fields the protocol defines -- provider-specific extras are dropped
// rather than replayed to an endpoint that may not recognise them.
func assistantMessage(response map[string]any) ChatMessage {
	raw := assistantRaw(response)
	content, _ := raw["content"].(string)
	message := ChatMessage{Role: "assistant", Content: content}
	if calls, ok := raw["tool_calls"]; ok {
		if encoded, err := json.Marshal(calls); err == nil {
			message.ToolCalls = encoded
		}
	}
	return message
}

// SpawnAPIChild is the RunnerAPI arm of DispatchToRunner.
//
// Unlike the other runners it spawns no child process at all: it drives a
// chat endpoint and executes the tool calls itself, inside the sandbox. That
// is the whole point of runner="api" -- it serves deployments where no
// coding CLI exists to spawn.
//
// Configuration is read from the caller rather than resolved here so this
// stays testable without a settings tree; DispatchToRunner supplies it.
type APIRunnerConfig struct {
	ProjectRoot      string
	BaseURL          string
	APIKeyEnvVar     string
	Model            string
	CommandAllowlist []string
	WritesAllowed    bool
}

// SpawnAPIChild runs one API dispatch and returns the same result shape the
// process-spawning runners do.
func SpawnAPIChild(ctx DispatchContext, config APIRunnerConfig, timeout time.Duration) map[string]any {
	model, err := ResolveModel(ctx.ModelTier, config.Model, ctx.RoleID)
	if err != nil {
		return map[string]any{"status": "unavailable", "reason": err.Error()}
	}
	endpoint, err := ResolveEndpoint(config.BaseURL, config.APIKeyEnvVar, model)
	if err != nil {
		return map[string]any{"status": "unavailable", "reason": err.Error()}
	}

	// Writes require both the role's own capability and the operator's
	// opt-in. Re-checked here rather than trusted from the caller's control
	// flow, so this module's write authorization does not depend on reading
	// that flow correctly.
	writesAllowed := config.WritesAllowed && ctx.IsWriteCapable

	box := &Toolbox{
		ProjectRoot:      config.ProjectRoot,
		WritesAllowed:    writesAllowed,
		CommandAllowlist: config.CommandAllowlist,
	}
	tools := buildToolSchemas(AvailableToolNames(writesAllowed, config.CommandAllowlist))

	messages := []ChatMessage{}
	if ctx.DeveloperInstructs != "" {
		messages = append(messages, ChatMessage{Role: "system", Content: ctx.DeveloperInstructs})
	}
	messages = append(messages, ChatMessage{Role: "user", Content: ctx.Prompt})

	result, err := RunAPIDispatch(context.Background(), endpoint, box, messages, tools, timeout)
	if err != nil {
		// Nothing was mutated -- see RunAPIDispatch. An endpoint outage is an
		// infrastructure failure, not a dispatch that ran and failed.
		return map[string]any{"status": "unavailable", "reason": err.Error()}
	}

	return map[string]any{
		"status":        "completed",
		"exit_code":     result.ExitCode,
		"stdout":        result.Transcript,
		"files_written": result.FilesWritten,
		"commands_run":  result.CommandsRun,
		"tool_calls":    result.ToolCalls,
		"timed_out":     result.TimedOut,
	}
}

// buildToolSchemas renders the available tool names as OpenAI function
// definitions.
func buildToolSchemas(names []string) []map[string]any {
	descriptions := map[string]string{
		"read_file":   "Read one file inside the project.",
		"list_files":  "List files inside the project, optionally filtered by a glob.",
		"search":      "Find lines containing a literal string across the project.",
		"write_file":  "Create or overwrite one file inside the project.",
		"edit_file":   "Replace the contents of one file inside the project.",
		"run_command": "Run one allowlisted command inside the project.",
	}
	properties := map[string]map[string]any{
		"read_file":  {"path": map[string]any{"type": "string"}},
		"list_files": {"pattern": map[string]any{"type": "string"}},
		"search":     {"query": map[string]any{"type": "string"}},
		"write_file": {"path": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}},
		"edit_file":  {"path": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}},
		"run_command": {
			"command": map[string]any{"type": "string"},
			"args":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
	}
	required := map[string][]string{
		"read_file": {"path"}, "search": {"query"},
		"write_file": {"path", "content"}, "edit_file": {"path", "content"},
		"run_command": {"command"},
	}

	var schemas []map[string]any
	for _, name := range names {
		schema := map[string]any{"type": "object", "properties": properties[name]}
		if fields, ok := required[name]; ok {
			schema["required"] = fields
		}
		schemas = append(schemas, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": name, "description": descriptions[name], "parameters": schema,
			},
		})
	}
	return schemas
}

// ResolveAPIRunnerConfig reads the runner's operator settings.
//
// A package variable so a test can supply configuration without a settings
// tree, and so the resolution lives in one place rather than being threaded
// through ExecuteDispatchChild's signature and every caller of it.
var ResolveAPIRunnerConfig = func(projectRoot string) APIRunnerConfig {
	ctx := context.Background()
	read := func(key string) string {
		value, err := cadreconfig.ResolveString(ctx, key)
		if err != nil {
			return ""
		}
		return value
	}

	config := APIRunnerConfig{
		ProjectRoot:  projectRoot,
		BaseURL:      read("runners.api_base_url"),
		APIKeyEnvVar: read("runners.api_key_env"),
	}

	// Writes are opt-in, and an unreadable or unset setting is "no". The
	// fail-closed direction matters more here than anywhere else in this
	// runner: the alternative is a model writing to a workspace whose
	// operator never enabled it.
	config.WritesAllowed = isTruthySetting(read("runners.api_allow_writes"))

	// An unconfigured allowlist means run_command is unavailable, not that
	// every command is permitted -- see AvailableToolNames.
	for _, entry := range strings.Split(read("runners.api_command_allowlist"), ",") {
		if trimmed := strings.TrimSpace(entry); trimmed != "" {
			config.CommandAllowlist = append(config.CommandAllowlist, trimmed)
		}
	}
	return config
}

// isTruthySetting mirrors the vocabulary settings.py accepts, so an operator
// who wrote "yes" in one place and "1" in another gets the same answer.
func isTruthySetting(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// modelForTier is the configured model for a role's tier.
func modelForTier(tier string) string {
	if tier == "" {
		return ""
	}
	value, err := cadreconfig.ResolveString(context.Background(), "runners.local_model_"+strings.ToLower(tier))
	if err != nil {
		return ""
	}
	return value
}

// apiProjectRoot is the workspace the API runner's tools are confined to.
//
// The dispatch server's own project root when it set one, else the working
// directory. Not a caller-supplied value: the sandbox's containment is
// relative to this, so a child able to name it could name its way out.
func apiProjectRoot() string {
	if root := os.Getenv("CADRE_PROJECT_ROOT"); root != "" {
		return root
	}
	if working, err := os.Getwd(); err == nil {
		return working
	}
	return "."
}
