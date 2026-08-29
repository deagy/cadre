package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deagy/cadre/cli/internal/engine/agents"
	"github.com/deagy/cadre/cli/internal/engine/executor"
	"github.com/deagy/cadre/cli/internal/engine/kernelfixture"
)

func kernelRoot(t *testing.T) string {
	t.Helper()
	working, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return kernelfixture.Root(t, filepath.Dir(filepath.Dir(filepath.Dir(working))))
}

func planRequest(t *testing.T, root, taskID, text string) PlanRequest {
	t.Helper()
	return PlanRequest{
		Root: root, KernelRoot: kernelRoot(t), TaskID: taskID, TaskText: text,
		Client: agents.FakeModelClient{}, Checkpointer: executor.NewMemoryCheckpointer(),
	}
}

// A task plans once against the repository's own shipped contracts.
func TestPlanningATaskAgainstTheShippedContracts(t *testing.T) {
	root := t.TempDir()

	engine, metadata, err := ExecutorForTask(planRequest(t, root,
		"task-1", "refactor the architecture of the billing service"))
	if err != nil {
		t.Fatalf("ExecutorForTask: %v", err)
	}
	if len(engine.Gates) == 0 {
		t.Fatal("the derived sequence is empty")
	}
	if metadata.TaskText == "" || metadata.AgentCatalogDigest == "" || metadata.CreatedAt == "" {
		t.Errorf("metadata = %+v, want the plan recorded", metadata)
	}
	if !TaskExists(root, "task-1") {
		t.Error("the task's plan was not written")
	}

	// Reconnecting needs no task text, and yields the same sequence.
	again, reconnected, err := ExecutorForTask(PlanRequest{
		Root: root, KernelRoot: kernelRoot(t), TaskID: "task-1",
		Client: agents.FakeModelClient{}, Checkpointer: executor.NewMemoryCheckpointer(),
	})
	if err != nil {
		t.Fatalf("reconnecting: %v", err)
	}
	if len(again.Gates) != len(engine.Gates) {
		t.Errorf("reconnected to %d gates, planned %d", len(again.Gates), len(engine.Gates))
	}
	if reconnected.TaskText != metadata.TaskText {
		t.Error("the recorded task text changed on reconnect")
	}
}

// A task's scope is fixed once planned.
//
// Replacing the text silently would leave every gate decision made so far
// attached to a task that now claims to be about something else.
func TestReplanningWithDifferentTextIsRefused(t *testing.T) {
	root := t.TempDir()
	if _, _, err := ExecutorForTask(planRequest(t, root, "task-1", "refactor the architecture")); err != nil {
		t.Fatalf("ExecutorForTask: %v", err)
	}

	_, _, err := ExecutorForTask(planRequest(t, root, "task-1", "something else entirely"))
	if err == nil {
		t.Fatal("a task's text was silently replaced")
	}
	if !strings.Contains(err.Error(), "different task text") {
		t.Errorf("error was %q", err)
	}
}

// A plan recorded against one catalog must not run against another.
func TestAChangedAgentCatalogIsRefused(t *testing.T) {
	root := t.TempDir()
	if _, _, err := ExecutorForTask(planRequest(t, root, "task-1", "refactor the architecture")); err != nil {
		t.Fatalf("ExecutorForTask: %v", err)
	}

	// Rewrite the recorded digest, as a catalog change would.
	metadata, _, err := ReadTaskConfig(root, "task-1")
	if err != nil {
		t.Fatalf("ReadTaskConfig: %v", err)
	}
	metadata.AgentCatalogDigest = "sha256:0000"
	if err := WriteTaskConfig(root, metadata); err != nil {
		t.Fatalf("WriteTaskConfig: %v", err)
	}

	_, _, err = ExecutorForTask(planRequest(t, root, "task-1", "refactor the architecture"))
	if err == nil {
		t.Fatal("a task ran against a catalog it was not planned against")
	}
	if !strings.Contains(err.Error(), "agent catalog") {
		t.Errorf("error was %q, want it to name the catalog", err)
	}
}

// Likewise a routing change that alters the derived sequence.
func TestAChangedGateSequenceIsRefused(t *testing.T) {
	root := t.TempDir()
	if _, _, err := ExecutorForTask(planRequest(t, root, "task-1", "refactor the architecture")); err != nil {
		t.Fatalf("ExecutorForTask: %v", err)
	}

	metadata, _, _ := ReadTaskConfig(root, "task-1")
	metadata.GateSequenceIDs = append(metadata.GateSequenceIDs, "G9")
	if err := WriteTaskConfig(root, metadata); err != nil {
		t.Fatalf("WriteTaskConfig: %v", err)
	}

	_, _, err := ExecutorForTask(planRequest(t, root, "task-1", "refactor the architecture"))
	if err == nil {
		t.Fatal("a task ran against a sequence it was not planned against")
	}
	if !strings.Contains(err.Error(), "gate sequence") {
		t.Errorf("error was %q, want it to name the sequence", err)
	}
}

// An unplanned task cannot be reconnected to.
func TestReconnectingToAnUnplannedTaskIsRefused(t *testing.T) {
	_, _, err := ExecutorForTask(PlanRequest{
		Root: t.TempDir(), KernelRoot: kernelRoot(t), TaskID: "never-planned",
		Client: agents.FakeModelClient{}, Checkpointer: executor.NewMemoryCheckpointer(),
	})
	if err == nil || !strings.Contains(err.Error(), "task text is required") {
		t.Errorf("error was %v, want a refusal naming the missing text", err)
	}
}

// Provider detection refuses to guess.
//
// Dispatching a gate's agents through a provider the operator did not choose
// is not a detail to infer, and the ambiguous case is when inferring is worst.
func TestModelProviderDetectionRefusesToGuess(t *testing.T) {
	for _, name := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "OPENAI_BASE_URL", OpenAIModelEnvVar} {
		t.Setenv(name, "")
	}

	if _, err := DetectModelProvider(); err == nil {
		t.Error("no credential configured was treated as a provider choice")
	} else if !strings.Contains(err.Error(), "no model provider configured") {
		t.Errorf("error was %q", err)
	}

	t.Setenv("ANTHROPIC_API_KEY", "a")
	if got, err := DetectModelProvider(); err != nil || got != "anthropic" {
		t.Errorf("DetectModelProvider = (%q, %v), want anthropic", got, err)
	}

	t.Setenv("OPENAI_API_KEY", "b")
	if _, err := DetectModelProvider(); err == nil {
		t.Error("two configured credentials were resolved without asking")
	} else if !strings.Contains(err.Error(), "disambiguate") {
		t.Errorf("error was %q", err)
	}

	t.Setenv("ANTHROPIC_API_KEY", "")
	if got, err := DetectModelProvider(); err != nil || got != "openai" {
		t.Errorf("DetectModelProvider = (%q, %v), want openai", got, err)
	}
}

// The fake client is reachable without any credential, so a dry run needs none.
func TestTheFakeModelClientNeedsNoCredential(t *testing.T) {
	for _, name := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "OPENAI_BASE_URL", OpenAIModelEnvVar} {
		t.Setenv(name, "")
	}
	t.Setenv(FakeModelEnvVar, "1")

	client, err := DefaultModelClient(nil)
	if err != nil {
		t.Fatalf("DefaultModelClient: %v", err)
	}
	contribution, err := client.Complete(agents.CompletionRequest{AgentID: "a", Kind: "author", GateID: "G1"})
	if err != nil || contribution.ArtifactID == "" {
		t.Errorf("the fake client produced (%+v, %v)", contribution, err)
	}
}

// Authorities start empty, so requirements resolve as unknown rather than
// silently applicable.
func TestInitialStateLeavesAuthoritiesUnassigned(t *testing.T) {
	initial := InitialState("task-1", "do the thing", "", "", "")
	if initial.Classification != "internal" {
		t.Errorf("classification = %q, want the internal default", initial.Classification)
	}
	if len(initial.Authorities) != 0 {
		t.Errorf("authorities = %v, want none assigned", initial.Authorities)
	}
	if initial.IntentRecordID != nil || initial.RequirementsBaselineID != nil {
		t.Error("an unsupplied source id was recorded as present")
	}

	withSource := InitialState("t", "x", "confidential", "gitlab-issue:g/p:issues/1", "")
	if withSource.IntentRecordID == nil || *withSource.IntentRecordID == "" {
		t.Error("a supplied intent record was dropped")
	}
	if withSource.Classification != "confidential" {
		t.Errorf("classification = %q", withSource.Classification)
	}
}
