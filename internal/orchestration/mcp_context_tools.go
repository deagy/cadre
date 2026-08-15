package orchestration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/deagy/cadre/cli/internal/contextstore"
)

// The four context-store tools the MCP dispatch server exposes:
// context_put, context_get, context_list, context_search.
//
// A port of roster/orchestration/mcp/context_tools.py. The store itself is
// already Go (internal/contextstore), so what is ported here is the *gate* in
// front of it -- identity resolution, the classification ceiling, the source
// partition and the audit record. Those are the parts that decide what a
// dispatched child may read and write, and they are why this is not simply a
// pass-through to the store's own API.
//
// Python shells out to the context CLI per call. This calls the store
// in-process: the subprocess boundary existed to run a *Python* CLI, and
// re-spawning this same binary to reach a package it already links would add
// a process and a serialisation round-trip without adding isolation. The
// relay protections that boundary carried -- the output size cap in
// particular -- are kept explicitly below.

const (
	// RoleIDEnvVar carries the role a dispatched child is acting as. Ambient
	// identity beats the asserted parameter: a child that could name itself
	// could write under any role's scope.
	RoleIDEnvVar = "SECURE_CLOUD_AGENTS_ROLE_ID"

	// TaskIDEnvVar makes every entry attributable to a task.
	TaskIDEnvVar = "SECURE_CLOUD_AGENTS_TASK_ID"

	// parentClassificationEnvVar carries the session's classification ceiling.
	// Set by the dispatch server for its children; a caller that could set it
	// would be asserting its own ceiling.
	parentClassificationEnvVar = "SECURE_CLOUD_AGENTS_PARENT_CLASSIFICATION"

	// MaxContextRelayBytes caps what a tool will hand back to a model. The
	// Python original enforced this on the CLI subprocess's stdout; the same
	// ceiling applies here to the marshalled result.
	MaxContextRelayBytes = 1 << 20
)

// ContextToolError is a refusal from the gate in front of the store. It is
// returned to the model as a tool error, never as a JSON-RPC fault.
type ContextToolError struct{ message string }

func (e *ContextToolError) Error() string { return e.message }

func contextToolErrorf(format string, args ...any) error {
	return &ContextToolError{message: fmt.Sprintf(format, args...)}
}

// resolvedAgent prefers ambient role identity over the asserted parameter.
//
// The parameter exists for a caller with no dispatch environment; where the
// environment does say who this is, that wins. A child able to override it
// could write into another role's scope, which is the whole point of scoping.
func resolvedAgent(agent string) (string, error) {
	if ambient := os.Getenv(RoleIDEnvVar); ambient != "" {
		return ambient, nil
	}
	if agent == "" {
		return "", contextToolErrorf(
			"agent is required: pass the role id you are acting as, or set %s "+
				"in the dispatch environment.", RoleIDEnvVar)
	}
	return agent, nil
}

// resolvedTaskID refuses rather than inventing one: an unattributable entry
// is worse than a failed call.
func resolvedTaskID(taskID string) (string, error) {
	if taskID == "" {
		if ambient := os.Getenv(TaskIDEnvVar); ambient != "" {
			return ambient, nil
		}
		return "", contextToolErrorf(
			"No task identifier is available: set %s in this session before "+
				"using the context store, so every entry is attributable.", TaskIDEnvVar)
	}
	return taskID, nil
}

// checkedClassification is narrow-only against the session's ceiling.
//
// It reuses the dispatch path's own rule rather than a second, weaker check:
// an entry written above the ceiling is exactly the labelling error dispatch
// already refuses. With no ceiling set it refuses outright -- otherwise a
// caller asserts its own ceiling, which is not a ceiling.
func checkedClassification(classification, parentClassification string) (string, error) {
	if parentClassification == "" {
		return "", contextToolErrorf(
			"This server must set %s before the context store is usable, so a "+
				"caller cannot assert a classification ceiling for itself.",
			parentClassificationEnvVar)
	}
	if classification == "" {
		classification = "internal"
	}
	resolved, err := ValidateClassification(classification, parentClassification)
	if err != nil {
		return "", contextToolErrorf("%s", err)
	}
	return resolved, nil
}

var contextSourceUnsafe = regexp.MustCompile(`[^a-z0-9._-]+`)

// DispatchSource is a stable partition derived from the project root, never
// from anything a child asserts. A child that could name its own source could
// read another project's entries.
func DispatchSource(projectRoot string) string {
	resolved := projectRoot
	if absolute, err := filepath.Abs(projectRoot); err == nil {
		resolved = absolute
	}
	sum := sha256.Sum256([]byte(resolved))
	digest := hex.EncodeToString(sum[:])[:12]

	name := strings.Trim(contextSourceUnsafe.ReplaceAllString(
		strings.ToLower(filepath.Base(resolved)), "-"), "-")
	if name == "" {
		name = "project"
	}
	return fmt.Sprintf("dispatch-%s-%s", name, digest)
}

// contextAudit records that this session reached for the store at all --
// a different question from what the store's own access_runs table saw.
//
// Never carries content, query text, or a label. BuildAuditRecord rejects the
// forbidden key set outright, and nothing here goes near it.
func (server *DispatchMCPServer) contextAudit(operation string, fields map[string]any) {
	if fields == nil {
		fields = map[string]any{}
	}
	fields["event"] = "context_" + operation
	record, err := BuildAuditRecord(fields)
	if err != nil {
		// Best-effort: a rejected record means a forbidden key reached this
		// call, which is a bug worth failing loudly in tests but must not
		// take down a tool the model is mid-way through using.
		return
	}
	_ = WriteAuditLog(record)
}

// contextCaller assembles the gate's decisions once, so the four tools cannot
// drift in how they resolve identity or the ceiling.
func (server *DispatchMCPServer) contextCaller(
	agent, taskID, classification, source string,
) (contextstore.CallerOptions, error) {
	resolvedRole, err := resolvedAgent(agent)
	if err != nil {
		return contextstore.CallerOptions{}, err
	}
	resolvedTask, err := resolvedTaskID(taskID)
	if err != nil {
		return contextstore.CallerOptions{}, err
	}
	resolvedClass, err := checkedClassification(classification, os.Getenv(parentClassificationEnvVar))
	if err != nil {
		return contextstore.CallerOptions{}, err
	}
	if source == "" {
		source = DispatchSource(server.projectRoot)
	}
	return contextstore.CallerOptions{
		Agent:          resolvedRole,
		TaskID:         resolvedTask,
		Classification: resolvedClass,
		Source:         source,
	}, nil
}

// openContextStore resolves the store the same way `cadre context` does.
func openContextStore() (*contextstore.Config, func(), error) {
	cfg, _, err := contextstore.LoadConfig("")
	if err != nil {
		return nil, nil, contextToolErrorf("context store is unavailable: %s", err)
	}
	return cfg, func() {}, nil
}

// relay marshals a tool result, refusing to hand back more than the cap.
func relay(value any) (map[string]any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, contextToolErrorf("context store returned a result that could not be encoded")
	}
	if len(encoded) > MaxContextRelayBytes {
		return nil, contextToolErrorf(
			"context store returned more output than the tool will relay (%d bytes)", len(encoded))
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		// A non-object result (a bundle is always an object today) is wrapped
		// rather than dropped.
		return map[string]any{"result": json.RawMessage(encoded)}, nil
	}
	return decoded, nil
}

// fenceBundleContent wraps every returned entry's content as untrusted.
//
// Stored content returns to the parent model as this tool call's result --
// the same position a dispatched child's stdout occupies, and it gets the
// same treatment for the same reason. Written by an agent is not the same as
// trustworthy: an entry may be a faithful summary of a file that was itself
// hostile, which is exactly what the store's untrusted_inputs flag records.
//
// Nothing fenced these results before, although context_get's own tool
// description told the model they were fenced -- so retrieved content
// reached the parent as ordinary trusted text, and a description the code
// did not implement made that harder to notice, not easier.
//
// Only content is fenced. A listing carries metadata and no content at all,
// so fencing it would announce a danger that is not present and teach the
// model to read the marker as noise.
func fenceBundleContent(bundle map[string]any) map[string]any {
	results, ok := bundle["results"].([]any)
	if !ok {
		return bundle
	}
	for _, raw := range results {
		result, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if content, ok := result["content"].(string); ok {
			result["content"] = WrapUntrustedOutput(content)
		}
	}
	return bundle
}

// relayFenced is relay for the two tools that return stored content.
func relayFenced(value any) (map[string]any, error) {
	bundle, err := relay(value)
	if err != nil {
		return nil, err
	}
	return fenceBundleContent(bundle), nil
}

// ContextPutRequest is context_put's arguments.
type ContextPutRequest struct {
	Label          string   `json:"label"`
	Content        string   `json:"content"`
	Agent          string   `json:"agent"`
	TaskID         string   `json:"task_id"`
	DispatchID     string   `json:"dispatch_id"`
	Classification string   `json:"classification"`
	Scope          string   `json:"scope"`
	Tags           []string `json:"tags"`
	DerivedFrom    []string `json:"derived_from"`
	TTLDays        *int     `json:"ttl_days"`
	Source         string   `json:"source"`
}

// ContextPut stores working material and returns its handle.
func (server *DispatchMCPServer) ContextPut(request ContextPutRequest) (map[string]any, error) {
	caller, err := server.contextCaller(request.Agent, request.TaskID, request.Classification, request.Source)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(request.Label) == "" {
		return nil, contextToolErrorf("label is required")
	}
	if request.Content == "" {
		return nil, contextToolErrorf("content is required")
	}
	scope := request.Scope
	if scope == "" {
		scope = "agent"
	}

	cfg, done, err := openContextStore()
	if err != nil {
		return nil, err
	}
	defer done()
	db, err := contextstore.OpenStore(cfg.Database, true)
	if err != nil {
		return nil, contextToolErrorf("context store is unavailable: %s", err)
	}
	defer func() { _ = db.Close() }()

	result, err := contextstore.PutEntry(db, cfg, contextstore.PutOptions{
		Scope:           scope,
		Classification:  caller.Classification,
		Agent:           caller.Agent,
		TaskID:          caller.TaskID,
		Label:           request.Label,
		Source:          caller.Source,
		DispatchID:      request.DispatchID,
		Content:         request.Content,
		Tags:            request.Tags,
		DerivedFrom:     request.DerivedFrom,
		TTLDaysOverride: request.TTLDays,
	})
	if err != nil {
		return nil, contextToolErrorf("%s", err)
	}

	server.contextAudit("put", map[string]any{
		"agent": caller.Agent, "task_id": caller.TaskID,
		"classification": caller.Classification, "scope": scope,
	})
	return relay(result)
}

// ContextGetRequest is context_get's arguments.
type ContextGetRequest struct {
	Handle         string `json:"handle"`
	Agent          string `json:"agent"`
	TaskID         string `json:"task_id"`
	DispatchID     string `json:"dispatch_id"`
	Classification string `json:"classification"`
	Source         string `json:"source"`
}

// ContextGet reads one entry by handle.
func (server *DispatchMCPServer) ContextGet(request ContextGetRequest) (map[string]any, error) {
	caller, err := server.contextCaller(request.Agent, request.TaskID, request.Classification, request.Source)
	if err != nil {
		return nil, err
	}
	if request.Handle == "" {
		return nil, contextToolErrorf("handle is required")
	}
	caller.DispatchID = request.DispatchID

	cfg, done, err := openContextStore()
	if err != nil {
		return nil, err
	}
	defer done()
	db, err := contextstore.OpenStore(cfg.Database, true)
	if err != nil {
		return nil, contextToolErrorf("context store is unavailable: %s", err)
	}
	defer func() { _ = db.Close() }()

	bundle, err := contextstore.GetEntry(db, contextstore.GetOptions{
		Handle:        request.Handle,
		CallerOptions: caller,
	})
	if err != nil {
		return nil, contextToolErrorf("%s", err)
	}
	server.contextAudit("get", map[string]any{
		"agent": caller.Agent, "task_id": caller.TaskID,
		"classification": caller.Classification,
	})
	return relayFenced(bundle)
}

// ContextListRequest is context_list's arguments.
type ContextListRequest struct {
	Agent          string   `json:"agent"`
	TaskID         string   `json:"task_id"`
	DispatchID     string   `json:"dispatch_id"`
	Classification string   `json:"classification"`
	Source         string   `json:"source"`
	Scope          string   `json:"scope"`
	Tags           []string `json:"tags"`
	Top            string   `json:"top"`
	FilterAgent    string   `json:"filter_agent"`
	FilterTaskID   string   `json:"filter_task_id"`
}

// ContextList returns metadata only -- never stored content. That is what
// context_get is for, and it keeps a broad listing from becoming a bulk read.
func (server *DispatchMCPServer) ContextList(request ContextListRequest) (map[string]any, error) {
	caller, err := server.contextCaller(request.Agent, request.TaskID, request.Classification, request.Source)
	if err != nil {
		return nil, err
	}
	caller.DispatchID = request.DispatchID
	caller.Scope = request.Scope

	cfg, done, err := openContextStore()
	if err != nil {
		return nil, err
	}
	defer done()
	db, err := contextstore.OpenStore(cfg.Database, true)
	if err != nil {
		return nil, contextToolErrorf("context store is unavailable: %s", err)
	}
	defer func() { _ = db.Close() }()

	bundle, err := contextstore.ListEntries(db, contextstore.ListOptions{
		CallerOptions:    caller,
		FilterDispatchID: request.DispatchID,
		FilterAgent:      request.FilterAgent,
		FilterTaskID:     request.FilterTaskID,
		Tags:             request.Tags,
		Top:              request.Top,
	})
	if err != nil {
		return nil, contextToolErrorf("%s", err)
	}
	server.contextAudit("list", map[string]any{
		"agent": caller.Agent, "task_id": caller.TaskID,
		"classification": caller.Classification,
	})
	return relay(bundle)
}

// ContextSearchRequest is context_search's arguments.
type ContextSearchRequest struct {
	Query          string `json:"query"`
	Agent          string `json:"agent"`
	TaskID         string `json:"task_id"`
	DispatchID     string `json:"dispatch_id"`
	Classification string `json:"classification"`
	Source         string `json:"source"`
	Scope          string `json:"scope"`
	Top            string `json:"top"`
}

// ContextSearch ranks entries by similarity. Every access filter applies
// before ranking, in SQL, so a result set is never narrowed after the fact.
func (server *DispatchMCPServer) ContextSearch(request ContextSearchRequest) (map[string]any, error) {
	caller, err := server.contextCaller(request.Agent, request.TaskID, request.Classification, request.Source)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(request.Query) == "" {
		return nil, contextToolErrorf("query is required")
	}
	caller.DispatchID = request.DispatchID
	caller.Scope = request.Scope

	cfg, done, err := openContextStore()
	if err != nil {
		return nil, err
	}
	defer done()
	db, err := contextstore.OpenStore(cfg.Database, true)
	if err != nil {
		return nil, contextToolErrorf("context store is unavailable: %s", err)
	}
	defer func() { _ = db.Close() }()

	bundle, err := contextstore.SearchEntries(db, cfg, request.Query, contextstore.SearchOptions{
		CallerOptions: caller,
		Top:           request.Top,
	})
	if err != nil {
		return nil, contextToolErrorf("%s", err)
	}
	// The query text is deliberately absent from the audit record.
	server.contextAudit("search", map[string]any{
		"agent": caller.Agent, "task_id": caller.TaskID,
		"classification": caller.Classification,
	})
	return relayFenced(bundle)
}

// contextToolDefinitions are the four context-store tools, appended to the
// dispatch server's own. Kept beside their implementations so a tool added
// here without a definition -- or the reverse -- is a one-file diff.
func contextToolDefinitions() []MCPToolDefinition {
	callerProperties := func(extra map[string]any) map[string]any {
		properties := map[string]any{
			"agent": map[string]any{"type": "string",
				"description": "Role id you are acting as. Ignored when the dispatch environment names one."},
			"task_id": map[string]any{"type": "string",
				"description": "Task this entry belongs to. Required unless the environment names one."},
			"dispatch_id":    map[string]any{"type": "string", "description": "Dispatch this call belongs to."},
			"classification": map[string]any{"type": "string", "description": "internal, confidential or restricted. Never above the session ceiling."},
			"source":         map[string]any{"type": "string", "description": "Store partition. Defaults to this dispatch's own."},
		}
		for key, value := range extra {
			properties[key] = value
		}
		return properties
	}

	return []MCPToolDefinition{
		{
			Name:        "context_put",
			Description: "Park working material in the context store and get a handle back, so it does not have to sit in the conversation.",
			Schema: map[string]any{
				"type": "object",
				"properties": callerProperties(map[string]any{
					"label":        map[string]any{"type": "string", "description": "Short human label for this entry."},
					"content":      map[string]any{"type": "string", "description": "The material to store."},
					"scope":        map[string]any{"type": "string", "description": "agent, task or project."},
					"tags":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"derived_from": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"ttl_days":     map[string]any{"type": "integer"},
				}),
				"required": []string{"label", "content"},
			},
		},
		{
			Name:        "context_get",
			Description: "Read one context entry by handle. Content is returned fenced as untrusted material.",
			Schema: map[string]any{
				"type": "object",
				"properties": callerProperties(map[string]any{
					"handle": map[string]any{"type": "string", "description": "A ctx_... value from a prior context_put."},
				}),
				"required": []string{"handle"},
			},
		},
		{
			Name:        "context_list",
			Description: "List context entries you may read. Returns metadata only, never stored content -- context_get is for that.",
			Schema: map[string]any{
				"type": "object",
				"properties": callerProperties(map[string]any{
					"scope":          map[string]any{"type": "string"},
					"tags":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"top":            map[string]any{"type": "string"},
					"filter_agent":   map[string]any{"type": "string"},
					"filter_task_id": map[string]any{"type": "string"},
				}),
			},
		},
		{
			Name:        "context_search",
			Description: "Rank context entries by similarity to a query. Every access filter applies before ranking.",
			Schema: map[string]any{
				"type": "object",
				"properties": callerProperties(map[string]any{
					"query": map[string]any{"type": "string", "description": "What to look for."},
					"scope": map[string]any{"type": "string"},
					"top":   map[string]any{"type": "string"},
				}),
				"required": []string{"query"},
			},
		},
	}
}

// dispatchContextToolCall routes the four context tools. Returns false when
// toolName is not one of them, so the caller can fall through to its own.
func (server *DispatchMCPServer) dispatchContextToolCall(
	toolName string, args json.RawMessage,
) (*MCPToolResponse, bool) {
	decode := func(into any) *MCPToolResponse {
		if err := json.Unmarshal(args, into); err != nil {
			return &MCPToolResponse{Status: "error", IsError: true,
				Error: fmt.Sprintf("failed to parse arguments: %v", err)}
		}
		return nil
	}
	respond := func(result map[string]any, err error) *MCPToolResponse {
		if err != nil {
			// A refusal from the gate is a tool error the model can act on,
			// not a protocol fault.
			return &MCPToolResponse{Status: "error", IsError: true, Error: err.Error()}
		}
		return &MCPToolResponse{Status: "ok", Result: result}
	}

	switch toolName {
	case "context_put":
		var request ContextPutRequest
		if bad := decode(&request); bad != nil {
			return bad, true
		}
		return respond(server.ContextPut(request)), true
	case "context_get":
		var request ContextGetRequest
		if bad := decode(&request); bad != nil {
			return bad, true
		}
		return respond(server.ContextGet(request)), true
	case "context_list":
		var request ContextListRequest
		if bad := decode(&request); bad != nil {
			return bad, true
		}
		return respond(server.ContextList(request)), true
	case "context_search":
		var request ContextSearchRequest
		if bad := decode(&request); bad != nil {
			return bad, true
		}
		return respond(server.ContextSearch(request)), true
	}
	return nil, false
}
