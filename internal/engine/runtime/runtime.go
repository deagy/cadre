// Package runtime assembles the engine: contracts, provider, profile, gate
// sequence, model client and checkpoint store, into something a CLI or a
// service can drive.
//
// Ported from engine/agentic_sdlc_langgraph/runtime.py, with build_graph_for_task
// becoming ExecutorForTask -- there is no compiled graph to hold any more, so
// what it returns is the executor plus the metadata that pins how it was built.
package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/deagy/cadre/cli/internal/engine/agents"
	"github.com/deagy/cadre/cli/internal/engine/contracts"
	"github.com/deagy/cadre/cli/internal/engine/executor"
	"github.com/deagy/cadre/cli/internal/engine/planning"
	"github.com/deagy/cadre/cli/internal/engine/provider"
	"github.com/deagy/cadre/cli/internal/engine/state"
)

// TaskConfigSchemaVersion is the version stamped into a task's config.
const TaskConfigSchemaVersion = 2

// Environment variables selecting how agents are dispatched.
const (
	FakeModelEnvVar     = "AGENTIC_SDLC_LANGGRAPH_FAKE_MODEL"
	ModelProviderEnvVar = "AGENTIC_SDLC_LANGGRAPH_MODEL_PROVIDER"
	OpenAIModelEnvVar   = "AGENTIC_SDLC_LANGGRAPH_OPENAI_MODEL"
)

// ConfigError is a configuration fault the caller must resolve.
type ConfigError struct{ Message string }

func (e ConfigError) Error() string { return e.Message }

func configErrorf(format string, args ...any) error {
	return ConfigError{Message: fmt.Sprintf(format, args...)}
}

// Paths under a project root.
func agenticDir(root string) string { return filepath.Join(root, ".agentic-sdlc") }
func runsDir(root string) string    { return filepath.Join(agenticDir(root), "runs") }

// TaskConfigPath is where a task's plan is recorded.
func TaskConfigPath(root, taskID string) string {
	return filepath.Join(runsDir(root), taskID, "graph-config.json")
}

// CheckpointPath is the run store for a project.
func CheckpointPath(root string) string {
	return filepath.Join(agenticDir(root), "checkpoints.sqlite3")
}

// TaskExists reports whether a task has been planned under root.
func TaskExists(root, taskID string) bool {
	info, err := os.Stat(TaskConfigPath(root, taskID))
	return err == nil && !info.IsDir()
}

// DetectModelProvider chooses a provider from whichever credential is present.
//
// Neither guessing nor defaulting: with both credentials configured, or with
// none, this refuses. Anthropic is deliberately not a fallback -- dispatching
// a gate's agents through a provider the operator did not choose is not a
// detail to infer, and the ambiguous case is exactly when inferring is worst.
func DetectModelProvider() (string, error) {
	hasAnthropic := os.Getenv("ANTHROPIC_API_KEY") != ""
	hasOpenAI := os.Getenv("OPENAI_API_KEY") != "" ||
		os.Getenv("OPENAI_BASE_URL") != "" ||
		os.Getenv(OpenAIModelEnvVar) != ""

	switch {
	case hasAnthropic && !hasOpenAI:
		return "anthropic", nil
	case hasOpenAI && !hasAnthropic:
		return "openai", nil
	case hasAnthropic && hasOpenAI:
		return "", configErrorf(
			"both ANTHROPIC_API_KEY and an OpenAI-compatible credential "+
				"(OPENAI_API_KEY/OPENAI_BASE_URL/%s) are configured -- set %s=anthropic or "+
				"%s=openai to disambiguate which one dispatch should use.",
			OpenAIModelEnvVar, ModelProviderEnvVar, ModelProviderEnvVar)
	}
	return "", configErrorf(
		"no model provider configured for agent dispatch. Set one of: %s=anthropic "+
			"(+ ANTHROPIC_API_KEY); %s=openai (+ OPENAI_API_KEY or OPENAI_BASE_URL, + %s) -- "+
			"this is also the path for Codex CLI or any other OpenAI-compatible endpoint; or "+
			"%s=1 for a network-free dry run. An external CLI agent can also be dispatched "+
			"per-agent via the agent catalog's transport: \"a2a\" + endpoint, independent of "+
			"this provider selection.",
		ModelProviderEnvVar, ModelProviderEnvVar, OpenAIModelEnvVar, FakeModelEnvVar)
}

// DefaultModelClient resolves the client agents are dispatched through.
//
// A fake client when the fake-model variable is set, otherwise the chosen or
// detected provider. The catalog is threaded through so an agent declaring an
// a2a transport still reaches its own endpoint whatever the default is.
func DefaultModelClient(agentCatalog map[string]contracts.AgentCatalogEntry) (agents.ModelClient, error) {
	catalog := map[string]map[string]any{}
	for agentID, entry := range agentCatalog {
		catalog[agentID] = map[string]any{"kind": entry.Kind}
	}

	if os.Getenv(FakeModelEnvVar) == "1" {
		return &agents.DispatchingClient{Default: agents.FakeModelClient{}, AgentCatalog: catalog}, nil
	}

	choice := os.Getenv(ModelProviderEnvVar)
	if choice == "" {
		detected, err := DetectModelProvider()
		if err != nil {
			return nil, err
		}
		choice = detected
	}

	var base agents.ModelClient
	switch choice {
	case "anthropic":
		base = agents.AnthropicClient{}
	case "openai":
		model := os.Getenv(OpenAIModelEnvVar)
		if model == "" {
			return nil, configErrorf("%s=openai requires %s to name a model", ModelProviderEnvVar, OpenAIModelEnvVar)
		}
		base = agents.OpenAICompatibleClient{Model: model}
	default:
		return nil, configErrorf("%s=%q is not a known provider; use anthropic or openai",
			ModelProviderEnvVar, choice)
	}
	return &agents.DispatchingClient{Default: base, AgentCatalog: catalog}, nil
}

// TaskMetadata is the on-disk record of how a task was planned.
type TaskMetadata struct {
	SchemaVersion      int      `json:"schema_version"`
	TaskID             string   `json:"task_id"`
	TaskText           string   `json:"task_text"`
	ProfileID          string   `json:"profile_id"`
	ProviderManifest   string   `json:"provider_manifest"`
	IgnoredGateIDs     []string `json:"ignored_gate_ids"`
	GateSequenceIDs    []string `json:"gate_sequence_ids"`
	CreatedAt          string   `json:"created_at"`
	AgentCatalogDigest string   `json:"agent_catalog_digest"`
}

// ReadTaskConfig loads a task's plan, reporting whether one exists.
func ReadTaskConfig(root, taskID string) (TaskMetadata, bool, error) {
	var metadata TaskMetadata
	contents, err := os.ReadFile(TaskConfigPath(root, taskID))
	if os.IsNotExist(err) {
		return metadata, false, nil
	}
	if err != nil {
		return metadata, false, err
	}
	if err := json.Unmarshal(contents, &metadata); err != nil {
		return metadata, false, configErrorf("task %s has an unreadable graph-config.json: %v", taskID, err)
	}
	if metadata.ProfileID == "" {
		metadata.ProfileID = "generic"
	}
	return metadata, true, nil
}

// WriteTaskConfig records a task's plan.
func WriteTaskConfig(root string, metadata TaskMetadata) error {
	path := TaskConfigPath(root, metadata.TaskID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	metadata.SchemaVersion = TaskConfigSchemaVersion
	encoded, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o644)
}

// Contracts is everything a run needs from the kernel and the provider.
type Contracts struct {
	AllGates      []contracts.Gate
	MutationGates []contracts.MutationGate
	AgentCatalog  map[string]contracts.AgentCatalogEntry
	Profile       contracts.Profile
	ProviderRoot  string
}

// LoadContracts resolves the kernel contracts and a provider's profile.
//
// The lifecycle and mutation gates always come from the kernel's own
// contracts, whichever provider is active: they are kernel contracts, not
// provider content, and letting a provider supply its own would let it decide
// which gates exist at all.
func LoadContracts(kernelRoot, providerManifest, profileID string) (Contracts, error) {
	var resolved Contracts

	allGates, err := contracts.LoadLifecycleGates(
		filepath.Join(kernelRoot, "kernel", "contracts", "lifecycle-gates.json"))
	if err != nil {
		return resolved, err
	}
	mutationGates, err := contracts.LoadMutationGates(
		filepath.Join(kernelRoot, "kernel", "contracts", "mutation-gates.json"))
	if err != nil {
		return resolved, err
	}
	resolved.AllGates, resolved.MutationGates = allGates, mutationGates

	providerRoot := filepath.Join(kernelRoot, "providers", "agentic-sdlc-defaults")
	if providerManifest != "" {
		loaded, err := provider.LoadProvider(providerManifest, nil)
		if err != nil {
			return resolved, err
		}
		catalog := map[string]contracts.AgentCatalogEntry{}
		for agentID, entry := range loaded.AgentCatalog {
			kind, _ := entry["kind"].(string)
			catalog[agentID] = contracts.AgentCatalogEntry{Kind: kind}
		}
		resolved.AgentCatalog = catalog
		providerRoot = filepath.Dir(providerManifest)
	} else {
		catalog, err := contracts.LoadAgentCatalog(filepath.Join(providerRoot, "agent-catalog.json"))
		if err != nil {
			return resolved, err
		}
		resolved.AgentCatalog = catalog
	}

	profile, err := contracts.LoadProfile(filepath.Join(providerRoot, "profiles", profileID, "profile.json"))
	if err != nil {
		return resolved, configErrorf("cannot load profile %q: %v", profileID, err)
	}
	resolved.Profile = profile
	resolved.ProviderRoot = providerRoot
	return resolved, nil
}

// PlanRequest describes a task to plan or reconnect to.
type PlanRequest struct {
	Root             string
	KernelRoot       string
	TaskID           string
	TaskText         string
	ProfileID        string
	ProviderManifest string
	IgnoredGateIDs   []string

	// Client and Checkpointer override the environment-derived defaults, so a
	// test never depends on process environment.
	Client       agents.ModelClient
	Checkpointer executor.Checkpointer
}

// ExecutorForTask builds, or rebuilds, the executor for a task.
//
// The first call plans: it derives the gate sequence and records everything
// needed to rebuild it. Later calls rebuild from that record and check it
// still holds.
//
// A task's scope is fixed once planned. Supplying different task text for an
// existing task id is refused rather than silently replacing it -- the
// recorded sequence, and every gate decision made against it, belong to the
// text that was planned.
func ExecutorForTask(request PlanRequest) (*executor.Executor, TaskMetadata, error) {
	var metadata TaskMetadata

	profileID := request.ProfileID
	if profileID == "" {
		profileID = "generic"
	}
	existing, planned, err := ReadTaskConfig(request.Root, request.TaskID)
	if err != nil {
		return nil, metadata, err
	}
	if planned {
		if request.TaskText != "" && request.TaskText != existing.TaskText {
			return nil, metadata, configErrorf(
				"task ID %q was planned with different task text; use a new task ID", request.TaskID)
		}
		profileID = existing.ProfileID
		if request.ProviderManifest == "" {
			request.ProviderManifest = existing.ProviderManifest
		}
		request.TaskText = existing.TaskText
		request.IgnoredGateIDs = existing.IgnoredGateIDs
	} else if request.TaskText == "" {
		return nil, metadata, configErrorf(
			"task %q has not been planned under %s; task text is required to plan a new task",
			request.TaskID, request.Root)
	}

	resolved, err := LoadContracts(request.KernelRoot, request.ProviderManifest, profileID)
	if err != nil {
		return nil, metadata, err
	}

	sequence, err := planning.DeriveGateSequence(
		request.TaskText, resolved.Profile.Routing, request.IgnoredGateIDs, resolved.AllGates)
	if err != nil {
		return nil, metadata, err
	}
	sequenceIDs := make([]string, 0, len(sequence))
	for _, gate := range sequence {
		sequenceIDs = append(sequenceIDs, gate.ID)
	}

	catalogDigest, err := provider.Fingerprint(resolved.AgentCatalog)
	if err != nil {
		return nil, metadata, err
	}

	if planned {
		// Both checks refuse rather than run a graph that differs from the one
		// the task was planned against. A run whose shape changed underneath
		// it produces a record describing work that was never dispatched.
		if strings.Join(sequenceIDs, ",") != strings.Join(existing.GateSequenceIDs, ",") {
			return nil, metadata, configErrorf(
				"task %q is stale: the recorded gate sequence %v no longer matches the "+
					"recomputed sequence %v for the same task text, profile and ignored gates -- "+
					"has the provider's routing changed since this task was planned?",
				request.TaskID, existing.GateSequenceIDs, sequenceIDs)
		}
		if existing.AgentCatalogDigest != "" && existing.AgentCatalogDigest != catalogDigest {
			return nil, metadata, configErrorf(
				"task %q is stale: the loaded agent catalog's digest %q no longer matches the "+
					"digest %q recorded at plan time -- has the agent catalog (an agent's "+
					"transport or endpoint, say) changed since this task was planned?",
				request.TaskID, catalogDigest, existing.AgentCatalogDigest)
		}
		metadata = existing
	} else {
		metadata = TaskMetadata{
			SchemaVersion: TaskConfigSchemaVersion,
			TaskID:        request.TaskID, TaskText: request.TaskText, ProfileID: profileID,
			ProviderManifest:   request.ProviderManifest,
			IgnoredGateIDs:     orEmpty(request.IgnoredGateIDs),
			GateSequenceIDs:    sequenceIDs,
			CreatedAt:          time.Now().UTC().Format(time.RFC3339Nano),
			AgentCatalogDigest: catalogDigest,
		}
		if err := WriteTaskConfig(request.Root, metadata); err != nil {
			return nil, metadata, err
		}
	}

	client := request.Client
	if client == nil {
		client, err = DefaultModelClient(resolved.AgentCatalog)
		if err != nil {
			return nil, metadata, err
		}
	}
	checkpointer := request.Checkpointer
	if checkpointer == nil {
		if err := os.MkdirAll(agenticDir(request.Root), 0o755); err != nil {
			return nil, metadata, err
		}
		store, err := executor.OpenSQLiteCheckpointer(CheckpointPath(request.Root))
		if err != nil {
			return nil, metadata, err
		}
		checkpointer = store
	}

	return &executor.Executor{
		Gates:         sequence,
		MutationGates: resolved.MutationGates,
		Profile:       resolved.Profile,
		AgentCatalog:  resolved.AgentCatalog,
		Client:        client,
		Checkpointer:  checkpointer,
	}, metadata, nil
}

// InitialState is a task's starting state.
//
// Authorities start empty, so every gate's requirements resolve as "unknown"
// until a caller supplies its own assignment. That is a recorded
// simplification rather than an oversight: nothing surfaces a way to assign
// authorities yet, and "unknown" is what makes validate report an approved
// gate as blocked-on-a-decision rather than passing it silently.
func InitialState(taskID, taskText, classification, intentRecordID, requirementsBaselineID string) state.SDLCState {
	if classification == "" {
		classification = "internal"
	}
	initial := state.SDLCState{
		TaskID: taskID, Scope: taskText, Classification: classification,
		LifecycleGates: map[string]state.GateState{},
		AgentOutputs:   map[string]map[string]any{},
		Authorities:    map[string]map[string]any{},
	}
	if intentRecordID != "" {
		initial.IntentRecordID = &intentRecordID
	}
	if requirementsBaselineID != "" {
		initial.RequirementsBaselineID = &requirementsBaselineID
	}
	return initial
}

func orEmpty(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// ResultPayload renders a run result as the small JSON shape both entry
// points emit, so a CLI and a service describe the same run identically.
func ResultPayload(result executor.Result) map[string]any {
	if result.Suspended != nil {
		return map[string]any{"status": "interrupted", "interrupt": result.Suspended.Payload}
	}
	return map[string]any{"status": "complete", "message": "no interrupt, run complete"}
}

// TaskRequest is a task to plan or reconnect to.
type TaskRequest struct {
	PlanRequest
	Classification         string
	IntentRecordID         string
	RequirementsBaselineID string
}

// CreateOrReconnectTask plans a task, or reports that it is already planned.
//
// Reconnecting deliberately does not re-invoke: a planned task already has a
// position, and running it again from the top would re-dispatch agents for
// gates that were already decided.
func CreateOrReconnectTask(request TaskRequest) (map[string]any, error) {
	alreadyPlanned := TaskExists(request.Root, request.TaskID)

	engine, metadata, err := ExecutorForTask(request.PlanRequest)
	if err != nil {
		return nil, err
	}
	if alreadyPlanned {
		return map[string]any{
			"status":        "already-planned",
			"gate_sequence": metadata.GateSequenceIDs,
		}, nil
	}

	result, err := engine.Start(request.TaskID, InitialState(
		request.TaskID, request.TaskText, request.Classification,
		request.IntentRecordID, request.RequirementsBaselineID))
	if err != nil {
		return nil, err
	}
	return ResultPayload(result), nil
}

// ResumeTask applies a human decision to a planned task.
func ResumeTask(request PlanRequest, decision map[string]any) (map[string]any, error) {
	engine, _, err := ExecutorForTask(request)
	if err != nil {
		return nil, err
	}
	result, err := engine.Resume(request.TaskID, decision)
	if err != nil {
		return nil, err
	}
	return ResultPayload(result), nil
}

// TaskStatus reports a planned task's current position without advancing it.
//
// Read-only on purpose: asking what a run is waiting for must never move it,
// or a status check would dispatch agents.
func TaskStatus(request PlanRequest) (map[string]any, error) {
	engine, metadata, err := ExecutorForTask(request)
	if err != nil {
		return nil, err
	}

	checkpoint, found, err := engine.Checkpointer.Load(request.TaskID)
	if err != nil {
		return nil, err
	}
	if !found {
		return map[string]any{
			"status":        "planned",
			"gate_sequence": metadata.GateSequenceIDs,
			"message":       "planned but not yet started",
		}, nil
	}

	payload := map[string]any{
		"task_id":         request.TaskID,
		"gate_sequence":   metadata.GateSequenceIDs,
		"lifecycle_gates": checkpoint.State.LifecycleGates,
		"run_halted":      checkpoint.State.RunHalted,
	}
	switch {
	case checkpoint.Pending != nil:
		payload["status"] = "interrupted"
		payload["interrupt"] = checkpoint.Pending.Payload
	case gatesSettled(metadata.GateSequenceIDs, checkpoint.State.LifecycleGates):
		payload["status"] = "complete"
	default:
		// Reset by a re-entry, or never advanced. Reporting "complete" here --
		// which is what deriving the answer from a nil pending value did --
		// tells an operator a reopened run is finished.
		payload["status"] = "ready"
		payload["message"] = "gates are pending; advance the run to dispatch them"
	}
	return payload, nil
}

// gatesSettled reports whether every gate in the sequence has been decided.
//
// Completeness is a property of the gates, not of whether a decision happens
// to be outstanding: a re-entry clears the pending decision *and* resets its
// gates, so "nothing is pending" and "the run is finished" are different
// claims and only the gates can answer the second.
func gatesSettled(sequence []string, gates map[string]state.GateState) bool {
	if len(sequence) == 0 {
		return false
	}
	for _, gateID := range sequence {
		gate, present := gates[gateID]
		if !present {
			return false
		}
		switch gate.Status {
		case "approved", "request-changes", "blocked":
		default:
			return false
		}
	}
	return true
}
