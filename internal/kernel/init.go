package kernel

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/deagy/cadre/cli/internal/canonicaljson"
)

// `agentic-sdlc init` -- create a project's overlay.
//
// Conservative on purpose, in two ways that shape everything below.
//
// It never overwrites. Every file is written only if absent, and the report
// says which were created and which were already there. A project's overlay
// accumulates decisions -- who holds which authority, which impact categories
// apply -- and an init that refreshed it would discard them on a re-run
// somebody meant as a no-op.
//
// And it initialises into a blocked state deliberately. Every authority is
// `unknown` with nobody assigned, every impact category is `unknown`, and the
// report says `ready: false` with the reason. A freshly initialised project is
// not ready to run a lifecycle, and the alternative -- defaulting authorities
// to somebody and impacts to "probably not applicable" -- is the kernel making
// exactly the decisions it exists to make a human make.

// InitRequest is one `init` invocation.
type InitRequest struct {
	Root           string
	Profile        string
	Extensions     []string
	ProjectID      string
	Classification string
	Runner         string
	DryRun         bool
}

// Initialize creates a project overlay and reports what it did.
func (r *Registry) Initialize(request InitRequest) (*orderedObject, error) {
	root, err := filepath.Abs(request.Root)
	if err != nil {
		return nil, err
	}
	if !request.DryRun {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return nil, err
		}
	}
	// Resolved after the directory exists, so a root being created now still
	// resolves the way every later command will resolve it.
	root, err = resolveExisting(root)
	if err != nil {
		return nil, err
	}

	detected := DetectRepository(root)
	profileID, err := r.resolveProfileID(request.Profile, detected)
	if err != nil {
		return nil, err
	}
	projectID := request.ProjectID
	if projectID == "" {
		projectID = filepath.Base(root)
	}
	classification := request.Classification
	if classification == "" {
		classification = "internal"
	}

	profile, documents, routing, err := r.initializationArtifacts(
		root, profileID, uniqueStrings(request.Extensions), projectID, classification, detected)
	if err != nil {
		return nil, err
	}

	overlay, err := ConfinedPath(root, Overlay)
	if err != nil {
		return nil, err
	}
	lockPath := filepath.Join(overlay, "version.lock")
	agentsPath, err := ConfinedPath(root, "AGENTS.md")
	if err != nil {
		return nil, err
	}

	if request.DryRun {
		return r.previewInitialization(root, profile, profileID, request.Runner,
			documents, overlay, lockPath, agentsPath, detected)
	}

	if err := os.MkdirAll(filepath.Join(overlay, "runs"), 0o755); err != nil {
		return nil, err
	}
	created := []any{}
	for _, document := range documents {
		path := filepath.Join(overlay, document.name)
		written, err := writeJSONIfAbsent(path, document.value)
		if err != nil {
			return nil, err
		}
		if written {
			created = append(created, Overlay+"/"+document.name)
		}
	}
	if _, err := os.Stat(lockPath); err != nil {
		lock, err := r.versionLock(profileID, profile, routing)
		if err != nil {
			return nil, err
		}
		if _, err := writeJSONIfAbsent(lockPath, lock); err != nil {
			return nil, err
		}
		created = append(created, Overlay+"/version.lock")
	}

	agentsStatus := "created"
	if _, err := os.Stat(agentsPath); err == nil {
		agentsStatus = "updated_managed_block"
	}
	if err := updateAgentsMarkdown(agentsPath); err != nil {
		return nil, err
	}

	wrappers := []any{}
	if profileID != "" {
		names, _, err := r.writeAgentWrappers(root, profile, request.Runner, false)
		if err != nil {
			return nil, err
		}
		wrappers = names
	}

	return ordered(
		"status", "initialized",
		"root", root,
		"profile", nullableString(profileID),
		"created", created,
		"agent_wrappers_created", wrappers,
		"agents_md", agentsStatus,
		"ready", false,
		"blockers", []any{"Human authorities and impact applicability require explicit decisions."},
	), nil
}

// previewInitialization is `--dry-run`: what would happen, having written
// nothing.
//
// It reports AGENTS.md too, even though the dry-run path never calls the
// updater. A preview that omitted the one file init modifies rather than
// creates would be reassuring about the wrong thing.
func (r *Registry) previewInitialization(
	root string, profile *orderedObject, profileID, runner string,
	documents []overlayDocument, overlay, lockPath, agentsPath string, detected DetectionReport,
) (*orderedObject, error) {
	wouldCreate, existing := []any{}, []any{}
	for _, document := range documents {
		target := filepath.Join(overlay, document.name)
		if _, err := os.Stat(target); err != nil {
			wouldCreate = append(wouldCreate, Overlay+"/"+document.name)
			continue
		}
		existing = append(existing, Overlay+"/"+document.name)
	}
	if _, err := os.Stat(lockPath); err != nil {
		wouldCreate = append(wouldCreate, Overlay+"/version.lock")
	} else {
		existing = append(existing, Overlay+"/version.lock")
	}

	wrappersWouldCreate, wrappersExisting := []any{}, []any{}
	if profileID != "" {
		created, present, err := r.writeAgentWrappers(root, profile, runner, true)
		if err != nil {
			return nil, err
		}
		wrappersWouldCreate, wrappersExisting = created, present
	}

	agentsStatus := "would_create"
	if _, err := os.Stat(agentsPath); err == nil {
		agentsStatus = "would_update_managed_block"
	}
	return ordered(
		"status", "dry-run",
		"mutation", false,
		"root", root,
		"profile", nullableString(profileID),
		"would_create", wouldCreate,
		"existing_unchanged", existing,
		"agent_wrappers_would_create", wrappersWouldCreate,
		"agent_wrappers_existing", wrappersExisting,
		"agents_md", agentsStatus,
		"detected", detectionValue(detected),
	), nil
}

// resolveProfileID turns the --profile argument into a profile id or "".
//
// "kernel-only" and an omitted flag both mean no profile: a project can adopt
// the lifecycle without adopting anyone's role catalog. "auto" takes the
// detector's proposal, which is why the detector proposes rather than decides.
func (r *Registry) resolveProfileID(requested string, detected DetectionReport) (string, error) {
	switch requested {
	case "", "kernel-only":
		return "", nil
	case "auto":
		return detected.ProposedProfile, nil
	default:
		return requested, nil
	}
}

type overlayDocument struct {
	name  string
	value any
}

// initializationArtifacts builds every overlay document without writing one.
//
// `init` and `repair` share this builder deliberately: keeping creation and
// repair on one representation is what stops a repair from becoming a second,
// subtly different initializer.
func (r *Registry) initializationArtifacts(
	root, profileID string, extensionIDs []string, projectID, classification string,
	detected DetectionReport,
) (*orderedObject, []overlayDocument, map[string]any, error) {
	profile := ordered(
		"id", "kernel-only", "routing", []any{}, "ignored_gates", []any{},
		"gate_bindings", []any{}, "impact_categories", []any{},
	)
	if profileID != "" {
		merged, err := r.MergeProfile(profileID)
		if err != nil {
			return nil, nil, nil, err
		}
		profile = merged
	}

	impact := []any{}
	for _, raw := range listOf(profile.values["impact_categories"]) {
		if id, ok := raw.(string); ok {
			impact = append(impact, impactItem(id, "generic-software"))
		}
	}
	specializedBOMs := []any{}
	for _, extensionID := range extensionIDs {
		extension, err := r.loadExtension(extensionID)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, raw := range listOf(extension["impact_categories"]) {
			if id, ok := raw.(string); ok {
				impact = append(impact, impactItem(id, extensionID))
			}
		}
		for _, raw := range listOf(extension["specialized_boms"]) {
			if id, ok := raw.(string); ok {
				specializedBOMs = append(specializedBOMs, impactItem(id, extensionID))
			}
		}
	}

	profileDigest, err := Fingerprint(profile)
	if err != nil {
		return nil, nil, nil, err
	}
	providerIDs := []any{}
	for _, provider := range r.Providers {
		providerIDs = append(providerIDs, provider.ID)
	}

	project := ordered(
		"schema_version", 1,
		"project_id", projectID,
		"classification", classification,
		"profile", nullableString(profileID),
		"profile_digest", profileDigest,
		"extensions", asJSONList(extensionIDs),
		"providers", providerIDs,
		"approval_sources", ordered(
			"human_gate_default", "manual", "allow_manual_fallback", true),
		"detected", detectionValue(detected),
		"environments", []any{ordered(
			"name", "local", "persistence", "unknown", "production", "unknown")},
	)

	// Every authority starts unassigned, and a conditional one starts with its
	// applicability unresolved. Both are questions for a human, and recording
	// a guess here would answer them silently.
	//
	// Written in AuthorityRoleOrder rather than alphabetically: required roles
	// first, then the conditional ones, which is the order somebody filling
	// this file in works through it.
	authorities := &orderedObject{values: map[string]any{}}
	for _, role := range AuthorityRoleOrder {
		applicability := "applicable"
		if _, conditional := ConditionalAuthorityRoles[role]; conditional {
			applicability = "unknown"
		}
		authorities.set(role, ordered(
			"status", "unknown",
			"assignee", nil,
			"applicability", applicability,
			"rationale", nil,
			"evidence_reference", nil,
			"gates", asJSONList(AuthorityRoles[role]),
		))
	}

	blocking := []any{}
	for _, raw := range append(append([]any{}, impact...), specializedBOMs...) {
		if item, ok := raw.(*orderedObject); ok {
			blocking = append(blocking, item.values["id"])
		}
	}
	impactProfile := ordered(
		"profile_id", projectID+"-impact",
		"status", "blocked",
		"impact_categories", impact,
		"specialized_boms", specializedBOMs,
		"blocking_unknowns", blocking,
	)

	routing := ordered(
		"version", 1,
		"profile", nullableString(profileID),
		"routes", orDefault(profile.values["routing"], []any{}),
		"change_intake", orDefault(profile.values["change_intake"], map[string]any{}),
		"ignored_gates", orDefault(profile.values["ignored_gates"], []any{}),
		"gate_bindings", orDefault(profile.values["gate_bindings"], map[string]any{}),
	)
	commands := ordered(
		"version", 1,
		"commands", detectionField(detected, "command_candidates"),
		"confirmed", false,
	)

	return profile, []overlayDocument{
		{"project.json", project},
		{"authorities.json", authorities},
		{"impact-profile.json", impactProfile},
		{"routing.json", routing},
		{"commands.json", commands},
	}, plainRouting(routing), nil
}

// plainRouting is the routing document as a plain map, for the digest the
// version lock takes over its gate bindings.
func plainRouting(routing *orderedObject) map[string]any {
	plain, _ := plainValue(routing).(map[string]any)
	return plain
}

// impactItem is one unresolved impact category or specialized BOM.
func impactItem(id, extension string) *orderedObject {
	return ordered(
		"id", id,
		"extension", extension,
		"applicability", "unknown",
		"definition_reference", nil,
		"rationale", nil,
		"owner", nil,
		"evidence_refs", []any{},
	)
}

// versionLock records what this project was initialised against, so a later
// `upgrade` or `validate` can tell whether the ground has moved.
func (r *Registry) versionLock(
	profileID string, profile *orderedObject, routing map[string]any,
) (*orderedObject, error) {
	contractDigest, err := lifecycleContractDigest()
	if err != nil {
		return nil, err
	}
	profileDigest, err := Fingerprint(profile)
	if err != nil {
		return nil, err
	}
	bindingDigest, err := Fingerprint(orDefault(routing["gate_bindings"], map[string]any{}))
	if err != nil {
		return nil, err
	}
	return ordered(
		"plugin_version", Version,
		"kernel_version", Version,
		"contracts", 2,
		"contract_digest", contractDigest,
		"profile", nullableString(profileID),
		"profile_digest", profileDigest,
		"dispatch_binding_digest", bindingDigest,
		"providers", providerRecords(r.Providers),
	), nil
}

// loadExtension reads and checks one impact-profile extension.
func (r *Registry) loadExtension(extensionID string) (map[string]any, error) {
	for _, root := range r.ExtensionRoots {
		path := filepath.Join(root, extensionID, "extension.json")
		if _, err := os.Stat(path); err != nil {
			continue
		}
		extension, err := loadJSONObject(path)
		if err != nil {
			return nil, err
		}
		version, _ := extension["version"].(string)
		schemaVersion, _ := jsonNumber(extension["schema_version"])
		if schemaVersion != 1 || extension["id"] != extensionID || version == "" {
			return nil, fmt.Errorf("extension %s has malformed metadata", extensionID)
		}
		return extension, nil
	}
	return nil, fmt.Errorf("unknown extension: %s", extensionID)
}

// nullableString renders an empty id as JSON null rather than "".
//
// The distinction is real in the overlay: `"profile": null` means a project
// adopted the lifecycle without a role catalog, while `"profile": ""` is a
// profile whose name is the empty string.
func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func writeJSONIfAbsent(path string, value any) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	}
	return true, writeJSONDocument(path, value)
}

// Managed-block markers. Everything between them is ours to rewrite; anything
// outside is the project's and is preserved exactly.
const (
	managedBlockStart = "<!-- agentic-sdlc:start -->"
	managedBlockEnd   = "<!-- agentic-sdlc:end -->"
)

func managedAgentsBlock() string {
	return strings.Join([]string{
		managedBlockStart,
		"## Agentic SDLC",
		"",
		"This repository uses the portable Agentic SDLC project overlay in `.agentic-sdlc/`.",
		"Use its orchestration skill or CLI for multi-role delivery work. Run records are authoritative.",
		"Never infer gate approval, production/destructive authority, risk acceptance, or compliance applicability.",
		"Artifact authors must remain separate from independent reviewers and human approvers.",
		managedBlockEnd,
	}, "\n")
}

// updateAgentsMarkdown rewrites only the managed block.
func updateAgentsMarkdown(path string) error {
	existing := ""
	if data, err := os.ReadFile(path); err == nil {
		existing = string(data)
	}
	rendered, err := RenderAgentsMarkdown(existing)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(rendered), 0o644)
}

// RenderAgentsMarkdown returns AGENTS.md with our managed block updated.
//
// One marker without the other is refused rather than repaired. A file with a
// start and no end has had something done to it that this function cannot
// reason about, and guessing where the block ends would delete whatever the
// project wrote after it.
func RenderAgentsMarkdown(existing string) (string, error) {
	block := managedAgentsBlock()
	hasStart := strings.Contains(existing, managedBlockStart)
	hasEnd := strings.Contains(existing, managedBlockEnd)
	if hasStart != hasEnd {
		return "", fmt.Errorf("AGENTS.md contains an incomplete Agentic SDLC managed block")
	}
	if !hasStart {
		if strings.TrimSpace(existing) == "" {
			return block + "\n", nil
		}
		return strings.TrimRight(existing, " \t\n\r") + "\n\n" + block + "\n", nil
	}

	before, remainder, _ := strings.Cut(existing, managedBlockStart)
	_, after, _ := strings.Cut(remainder, managedBlockEnd)
	prefix := strings.TrimRight(before, " \t\n\r")
	suffix := strings.TrimLeft(after, "\n")

	content := ""
	if prefix != "" {
		content = prefix + "\n\n"
	}
	content += block
	if suffix != "" {
		return content + "\n" + suffix, nil
	}
	return content + "\n", nil
}

// writeAgentWrappers creates the per-runner subagent wrappers a profile's
// agents need.
//
// Existing wrappers are reported, never rewritten: a project may have tailored
// one, and re-running init is not a request to discard that.
func (r *Registry) writeAgentWrappers(
	root string, profile *orderedObject, runner string, dryRun bool,
) (created, existing []any, err error) {
	catalog, err := r.LoadAgentCatalog()
	if err != nil {
		return nil, nil, err
	}
	created, existing = []any{}, []any{}
	if runner == "" {
		runner = "both"
	}
	if runner == "codex" || runner == "both" {
		madeCodex, haveCodex, err := r.writeWrapperSet(
			root, profile, catalog, dryRun, ".codex", "toml", codexWrapper)
		if err != nil {
			return nil, nil, err
		}
		created, existing = append(created, madeCodex...), append(existing, haveCodex...)
	}
	if runner == "claude" || runner == "both" {
		madeClaude, haveClaude, err := r.writeWrapperSet(
			root, profile, catalog, dryRun, ".claude", "md", claudeWrapper)
		if err != nil {
			return nil, nil, err
		}
		created, existing = append(created, madeClaude...), append(existing, haveClaude...)
	}
	return created, existing, nil
}

func (r *Registry) writeWrapperSet(
	root string, profile *orderedObject, catalog map[string]any, dryRun bool,
	runnerDirectory, extension string,
	render func(agentID string, reviewer bool, metadata map[string]any, body string) string,
) (created, existing []any, err error) {
	directory, err := ConfinedPath(root, runnerDirectory, "agents")
	if err != nil {
		return nil, nil, err
	}
	if !dryRun {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return nil, nil, err
		}
	}

	created, existing = []any{}, []any{}
	for _, raw := range listOf(profile.values["agents"]) {
		agentID, ok := raw.(string)
		if !ok {
			continue
		}
		metadata, known := catalog[agentID].(map[string]any)
		if !known {
			continue
		}
		target := filepath.Join(directory, agentID+"."+extension)
		relative, err := filepath.Rel(root, target)
		if err != nil {
			return nil, nil, err
		}
		if _, err := os.Stat(target); err == nil {
			existing = append(existing, relative)
			continue
		}
		reviewer := metadata["kind"] == "reviewer"
		if !dryRun {
			body := agentWrapperBody(agentID, reviewer, metadata, profile)
			if err := os.WriteFile(target,
				[]byte(render(agentID, reviewer, metadata, body)), 0o644); err != nil {
				return nil, nil, err
			}
		}
		created = append(created, relative)
	}
	return created, existing, nil
}

func codexWrapper(agentID string, reviewer bool, metadata map[string]any, body string) string {
	sandbox := "workspace-write"
	if reviewer {
		sandbox = "read-only"
	}
	return strings.Join([]string{
		"name = " + tomlString(agentID),
		"description = " + tomlString(wrapperDescription(metadata)),
		"sandbox_mode = " + tomlString(sandbox),
		"developer_instructions = " + tomlString(body),
		"",
	}, "\n")
}

func claudeWrapper(agentID string, reviewer bool, metadata map[string]any, body string) string {
	tools := "Read, Grep, Glob, Bash, Edit, Write"
	if reviewer {
		// A reviewer that can write is not a reviewer. The tool list is the
		// enforcement, not the instruction text above it.
		tools = "Read, Grep, Glob, Bash"
	}
	frontmatter := strings.Join([]string{
		"---",
		"name: " + agentID,
		"description: " + wrapperDescription(metadata),
		"tools: " + tools,
		"---",
		"",
	}, "\n")
	return frontmatter + body + "\n"
}

func wrapperDescription(metadata map[string]any) string {
	kind, _ := metadata["kind"].(string)
	if kind == "" {
		kind = "specialist"
	}
	phase, _ := metadata["phase"].(string)
	if phase == "" {
		phase = "lifecycle"
	}
	return "Portable Agentic SDLC " + kind + " for " + phase
}

// tomlString reuses JSON string syntax, which TOML's basic strings match for
// everything these values contain -- including the \uXXXX escapes, which TOML
// reads the same way JSON does.
//
// Written with the Python-compatible escaper rather than encoding/json: these
// wrappers carry role definitions written by people, and an em-dash in one of
// them is raw UTF-8 to Go and \u2014 to Python. Both are valid TOML and the
// files differ byte for byte, which is what a drift check compares.
func tomlString(value string) string {
	var builder strings.Builder
	canonicaljson.WriteString(&builder, value)
	return builder.String()
}

const askHumanRule = "You are a dispatched subagent: you cannot ask the human directly. " +
	"If you reach a decision only a human can make, stop and return a clearly labeled " +
	"blocking question in your result instead of guessing or proceeding."

const richContentAdaptationNote = "Adapted from a cloud/GitLab-specific role definition " +
	"bundled with secure-cloud-agents. Its shared-policy references (agents/shared/*.md paths) " +
	"belong to that source repository and will not resolve here — review and tailor this role " +
	"for this project's own stack, policies, and gates before relying on it."

// agentWrapperBody is what the dispatched subagent is told.
//
// The two rules that survive every variant: never approve a gate, and never
// pretend to be able to ask the human. A subagent that thinks it can ask will
// guess instead of blocking.
func agentWrapperBody(
	agentID string, reviewer bool, metadata map[string]any, profile *orderedObject,
) string {
	if profile.values["rich_content_source"] != nil {
		if rich := richAgentContent(metadata); rich != "" {
			return strings.Join([]string{rich, richContentAdaptationNote, askHumanRule}, "\n\n")
		}
	}
	independence := "Prepare artifacts for independent review; do not self-review."
	if reviewer {
		independence = "Remain independent and do not modify the artifact under review."
	}
	return fmt.Sprintf(
		"Act as the portable Agentic SDLC role %s. "+
			"Bind work to the task revision and lifecycle gate. "+
			"Never approve a lifecycle or mutation gate. "+
			"%s %s", agentID, independence, askHumanRule)
}

func richAgentContent(metadata map[string]any) string {
	definition, _ := metadata["definition"].(string)
	if definition == "" {
		return ""
	}
	data, err := os.ReadFile(definition)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// detectionValue renders a detection report as the ordered JSON value the
// overlay embeds. Round-tripped through the struct's own field order, which is
// the Python dict's insertion order.
func detectionValue(detected DetectionReport) any {
	encoded, err := json.Marshal(detected)
	if err != nil {
		return map[string]any{}
	}
	value, err := DecodeOrdered(encoded)
	if err != nil {
		return map[string]any{}
	}
	return value
}

// detectionField pulls one field out of that rendered form, so the value
// embedded in commands.json is the same shape as the one in project.json.
func detectionField(detected DetectionReport, field string) any {
	value, ok := detectionValue(detected).(*orderedObject)
	if !ok {
		return nil
	}
	return value.values[field]
}
