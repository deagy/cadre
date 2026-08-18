package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every generic role prompt carries the separation-of-duties line and the
// rule that a subagent must stop rather than guess.
//
// These are the two sentences that make a dispatched agent safe to run
// unattended, so they are asserted rather than assumed.
func TestGenericPromptsCarryTheSafetyRules(t *testing.T) {
	author := ResolveRolePrompt(RolePromptRequest{AgentID: "backend-engineer", Kind: "author"})
	reviewer := ResolveRolePrompt(RolePromptRequest{AgentID: "code-reviewer", Kind: "reviewer"})

	for name, prompt := range map[string]string{"author": author, "reviewer": reviewer} {
		if !strings.Contains(prompt, "Never approve a lifecycle or mutation gate.") {
			t.Errorf("%s prompt does not forbid approving a gate:\n%s", name, prompt)
		}
		if !strings.Contains(prompt, AskHumanRule) {
			t.Errorf("%s prompt omits the ask-human rule", name)
		}
	}

	if !strings.Contains(author, "do not self-review") {
		t.Errorf("the author prompt does not forbid self-review:\n%s", author)
	}
	if !strings.Contains(reviewer, "do not modify the artifact under review") {
		t.Errorf("the reviewer prompt does not forbid modifying the artifact:\n%s", reviewer)
	}
	// The two must not be interchangeable: an author told to "remain
	// independent" and a reviewer told to "prepare artifacts" would each be
	// given the other's duty.
	if author == reviewer {
		t.Error("author and reviewer prompts are identical")
	}
}

// A provider's definition is used only when the profile opts in.
func TestRichContentRequiresOptIn(t *testing.T) {
	root := t.TempDir()
	definition := filepath.Join(root, "role.md")
	if err := os.WriteFile(definition, []byte("  Provider role text.  "), 0o644); err != nil {
		t.Fatal(err)
	}

	metadata := map[string]any{"definition": definition}

	withoutOptIn := ResolveRolePrompt(RolePromptRequest{
		AgentID: "a", Kind: "author", Metadata: metadata, Profile: map[string]any{},
	})
	if strings.Contains(withoutOptIn, "Provider role text.") {
		t.Error("a provider definition was used without the profile opting in")
	}

	withOptIn := ResolveRolePrompt(RolePromptRequest{
		AgentID: "a", Kind: "author", Metadata: metadata,
		Profile: map[string]any{"rich_content_source": true},
	})
	if !strings.Contains(withOptIn, "Provider role text.") {
		t.Errorf("the opted-in definition was not used:\n%s", withOptIn)
	}
	// It came from another repository, so it must say so, and it still has to
	// carry the ask-human rule.
	if !strings.Contains(withOptIn, RichContentAdaptationNote) {
		t.Error("rich content was used without the adaptation note")
	}
	if !strings.Contains(withOptIn, AskHumanRule) {
		t.Error("rich content was used without the ask-human rule")
	}
}

// With no provider root, a relative definition is unresolved rather than
// resolved against the working directory.
//
// Resolving it would read whatever happens to sit at that path next to
// whoever ran the process -- an unconfined escape hatch reached by a catalog
// field.
func TestARelativeDefinitionIsNotResolvedAgainstTheWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "planted.md"), []byte("planted content"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	prompt := ResolveRolePrompt(RolePromptRequest{
		AgentID: "a", Kind: "author",
		Metadata: map[string]any{"definition": "planted.md"},
		Profile:  map[string]any{"rich_content_source": true},
	})
	if strings.Contains(prompt, "planted content") {
		t.Error("a relative definition was resolved against the working directory")
	}
	if !strings.Contains(prompt, "Act as the portable Agentic SDLC role") {
		t.Error("the generic instruction was not used as the fallback")
	}
}

// A definition escaping its provider root falls back rather than being read.
func TestADefinitionEscapingItsProviderRootIsRefused(t *testing.T) {
	providerRoot := t.TempDir()
	outside := filepath.Join(filepath.Dir(providerRoot), "outside-role.md")
	if err := os.WriteFile(outside, []byte("outside content"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(outside) })

	prompt := ResolveRolePrompt(RolePromptRequest{
		AgentID: "a", Kind: "author",
		Metadata:     map[string]any{"definition": "../" + filepath.Base(outside)},
		Profile:      map[string]any{"rich_content_source": true},
		ProviderRoot: providerRoot,
	})
	if strings.Contains(prompt, "outside content") {
		t.Error("a definition outside the provider root was read")
	}
}

// A missing definition falls back rather than failing the dispatch.
func TestAMissingDefinitionFallsBack(t *testing.T) {
	prompt := ResolveRolePrompt(RolePromptRequest{
		AgentID: "a", Kind: "author",
		Metadata: map[string]any{"definition": "/nonexistent/role.md"},
		Profile:  map[string]any{"rich_content_source": true},
	})
	if !strings.Contains(prompt, "Act as the portable Agentic SDLC role a.") {
		t.Errorf("a missing definition did not fall back:\n%s", prompt)
	}
}

func TestFakeModelClientIsDeterministic(t *testing.T) {
	client := FakeModelClient{BlockingAgents: map[string]bool{"blocked-agent": true}}
	request := CompletionRequest{AgentID: "backend-engineer", Kind: "author", GateID: "G3"}

	first, err := client.Complete(request)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	second, _ := client.Complete(request)
	if first != second {
		t.Errorf("two identical requests produced different contributions:\n  %+v\n  %+v", first, second)
	}
	if first.ArtifactID != "G3-backend-engineer-artifact" || first.Revision != "rev-1" {
		t.Errorf("contribution = %+v", first)
	}
	if first.BlockingQuestion != nil {
		t.Error("an unblocked agent returned a blocking question")
	}

	blocked, _ := client.Complete(CompletionRequest{AgentID: "blocked-agent", Kind: "author", GateID: "G3"})
	if blocked.BlockingQuestion == nil {
		t.Error("a blocking agent returned no question")
	}
}

// The tool contract is shared so two clients cannot drift on it.
func TestSubmitContributionSchema(t *testing.T) {
	schema := SubmitContributionSchema()
	if schema["additionalProperties"] != false {
		t.Error("the schema permits additional properties; a model could return anything")
	}
	required, _ := schema["required"].([]any)
	if len(required) != 3 {
		t.Errorf("required = %v, want artifact_id, revision and summary", required)
	}
	properties, _ := schema["properties"].(map[string]any)
	blocking, _ := properties["blocking_question"].(map[string]any)
	types, _ := blocking["type"].([]any)
	if len(types) != 2 {
		t.Errorf("blocking_question type = %v, want it nullable", types)
	}
}

// A model's claimed artifact id is provenance a human will read, so anything
// it cannot be trusted to assert is replaced rather than recorded.
//
// The control and format characters matter most: a zero-width space or a
// right-to-left override renders as nothing, or reverses what follows it, so
// an artifact id could claim one thing in a record and display as another.
func TestArtifactFieldsAreSanitized(t *testing.T) {
	const fallback = "G1-agent-artifact"

	kept := []string{"api-contract", "a/b/c.md", "rev-2", strings.Repeat("x", 200)}
	for _, value := range kept {
		if got := sanitizedArtifactField(value, fallback); got != value {
			t.Errorf("sanitizedArtifactField(%q) = %q, want it kept", value, got)
		}
	}

	replaced := map[string]string{
		"empty":            "",
		"too long":         strings.Repeat("x", 201),
		"newline":          "artifact\nid",
		"null byte":        "artifact\x00id",
		"zero-width space": "artifact​id",
		"rtl override":     "artifact‮id",
		"soft hyphen":      "artifact­id",
	}
	for name, value := range replaced {
		if got := sanitizedArtifactField(value, fallback); got != fallback {
			t.Errorf("%s: sanitizedArtifactField(%q) = %q, want the fallback", name, value, got)
		}
	}
}

// A dispatched agent's output binds its artifact to a digest over both the id
// and the revision, so a later claim about either is checkable.
func TestRunProducesBoundEvidence(t *testing.T) {
	output, err := Run(
		Dispatch{AgentID: "backend-engineer", Kind: "author"},
		DispatchRequest{GateID: "G3", TaskText: "do the thing", Classification: "internal"},
		FakeModelClient{},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if output.Identity.Kind != "agent" || output.Identity.Role != "author:backend-engineer" {
		t.Errorf("identity = %+v", output.Identity)
	}
	if output.ArtifactBinding.Digest == "" || !strings.HasPrefix(output.ArtifactBinding.Digest, "sha256:") {
		t.Errorf("artifact digest = %q", output.ArtifactBinding.Digest)
	}
	if output.EvidenceRef.Hash != strings.TrimPrefix(output.ArtifactBinding.Digest, "sha256:") {
		t.Error("the evidence hash does not match the artifact digest it is supposed to bind")
	}
	if output.EvidenceRef.URI != "agent-dispatch://G3/backend-engineer" {
		t.Errorf("evidence uri = %q", output.EvidenceRef.URI)
	}

	// The digest must actually depend on both fields.
	other, _ := Run(
		Dispatch{AgentID: "backend-engineer", Kind: "author"},
		DispatchRequest{GateID: "G4", TaskText: "do the thing"},
		FakeModelClient{},
	)
	if other.ArtifactBinding.Digest == output.ArtifactBinding.Digest {
		t.Error("two different artifacts produced the same digest")
	}
}

// A model returning a hostile artifact id must not get it into the record.
func TestAHostileArtifactIDDoesNotReachTheEvidence(t *testing.T) {
	hostile := hostileClient{artifactID: "real-artifact‮gnp.exe", revision: "rev\x001"}

	output, err := Run(
		Dispatch{AgentID: "a", Kind: "author"},
		DispatchRequest{GateID: "G1"},
		hostile,
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if output.ArtifactBinding.ArtifactID != "G1-a-artifact" {
		t.Errorf("artifact id = %q, want the fallback", output.ArtifactBinding.ArtifactID)
	}
	if output.ArtifactBinding.Revision != "rev-1" {
		t.Errorf("revision = %q, want the fallback", output.ArtifactBinding.Revision)
	}
}

type hostileClient struct{ artifactID, revision string }

func (h hostileClient) Complete(CompletionRequest) (AgentContribution, error) {
	return AgentContribution{ArtifactID: h.artifactID, Revision: h.revision, Summary: "ok"}, nil
}

// The digest must bind the revision, not just the artifact id.
//
// Varying the gate also varies the id, so a test that changes both proves
// only that *something* is hashed. Holding the id fixed and moving the
// revision is what shows the binding is real: without it, two revisions of
// the same artifact would carry identical evidence, and a claim about which
// revision a gate approved could not be checked.
func TestTheDigestBindsTheRevision(t *testing.T) {
	dispatch := Dispatch{AgentID: "a", Kind: "author"}
	request := DispatchRequest{GateID: "G1"}

	first, err := Run(dispatch, request, fixedClient{artifactID: "same-artifact", revision: "rev-1"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	second, err := Run(dispatch, request, fixedClient{artifactID: "same-artifact", revision: "rev-2"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if first.ArtifactBinding.ArtifactID != second.ArtifactBinding.ArtifactID {
		t.Fatal("the fixture varied the artifact id; this test must vary only the revision")
	}
	if first.ArtifactBinding.Digest == second.ArtifactBinding.Digest {
		t.Error("two revisions of the same artifact share a digest; the revision is not bound")
	}
}

type fixedClient struct{ artifactID, revision string }

func (f fixedClient) Complete(CompletionRequest) (AgentContribution, error) {
	return AgentContribution{ArtifactID: f.artifactID, Revision: f.revision, Summary: "ok"}, nil
}
