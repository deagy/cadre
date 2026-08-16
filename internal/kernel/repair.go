package kernel

import (
	"fmt"
	"strings"

	"github.com/deagy/cadre/cli/internal/canonicaljson"
)

// `agentic-sdlc repair` -- reconcile an incomplete or stale initialisation.
//
// The rule the whole command is built around: existing project artifacts are
// decisions, not generated cache files. Repair creates only what is *missing*
// and refreshes the one uniquely delimited AGENTS.md block. It never replaces
// existing JSON, wrappers, run records, or approvals.
//
// So it plans before it writes, and the plan has three lists:
//
//   actions   what it would create or refresh
//   protected what already exists and will be left exactly as it is
//   blockers  what it cannot safely reason about at all
//
// Any blocker stops everything, including the actions that would otherwise
// have been safe. A repair that fixed what it could while something else was
// unreadable would leave a project half-reconciled and report success.
//
// Two blockers deserve naming, because both are cases where the honest answer
// is "a human has to look at this":
//
//   - The provider profile has changed since this project was initialised.
//     Repair could regenerate the routing to match, and that would silently
//     re-route work a project made decisions about.
//   - The version lock's provenance has moved -- a different profile, digest,
//     or provider set. The lock exists to notice exactly that.

// managedOverlayDocuments are the JSON documents repair knows how to recreate.
var managedOverlayDocuments = []string{
	"project.json", "authorities.json", "impact-profile.json",
	"routing.json", "commands.json",
}

// lockProvenanceKeys must not change under a repair. They record which
// profile and providers this project was initialised against.
var lockProvenanceKeys = []string{
	"profile", "profile_digest", "dispatch_binding_digest", "providers",
}

// lockUpgradeKeys are the ones a repair may move forward: they describe the
// kernel doing the repairing, not the project being repaired.
var lockUpgradeKeys = []string{
	"plugin_version", "kernel_version", "contracts", "contract_digest",
}

// RepairRequest is one `repair` invocation.
type RepairRequest struct {
	Root   string
	Runner string
	Apply  bool
}

type repairNote struct {
	path   string
	detail string
}

// repairPlan is what repair worked out before deciding whether to write.
type repairPlan struct {
	actions   []repairNote
	protected []string
	blockers  []repairNote
}

func (p *repairPlan) act(path, action string) {
	p.actions = append(p.actions, repairNote{path, action})
}
func (p *repairPlan) block(path, reason string) {
	p.blockers = append(p.blockers, repairNote{path, reason})
}
func (p *repairPlan) keep(path string) { p.protected = append(p.protected, path) }

func (p *repairPlan) actionPaths() map[string]bool {
	paths := map[string]bool{}
	for _, action := range p.actions {
		paths[action.path] = true
	}
	return paths
}

// Repair inspects a project and, with Apply, reconciles it.
func (r *Registry) Repair(request RepairRequest) (*orderedObject, int, error) {
	plan := &repairPlan{}
	filesystem, err := OpenRepairFilesystem(request.Root)
	if err != nil {
		plan.block(Overlay, "no safe existing initialization: "+err.Error())
		return repairReport("blocked", request.Root, false, plan), 1, nil
	}
	defer func() { _ = filesystem.Close() }()

	// The overlay directory itself must exist and be a real directory. A
	// repair against a project that was never initialised is an init, and
	// saying so is more useful than creating half of one.
	if _, err := filesystem.FileState(Overlay, "project.json"); err != nil {
		plan.block(Overlay, "no safe existing initialization: "+err.Error())
		return repairReport("blocked", filesystem.Root(), false, plan), 1, nil
	}

	loaded := map[string]map[string]any{}
	for _, name := range managedOverlayDocuments {
		state, err := filesystem.FileState(Overlay, name)
		if err != nil {
			plan.block(Overlay+"/"+name, "unsafe or unreadable managed JSON: "+err.Error())
			continue
		}
		if state != "regular" {
			continue
		}
		text, err := filesystem.ReadText(Overlay, name)
		if err != nil {
			plan.block(Overlay+"/"+name, "unsafe or unreadable managed JSON: "+err.Error())
			continue
		}
		document, err := decodeJSONObject([]byte(text))
		if err != nil {
			plan.block(Overlay+"/"+name, "unsafe or unreadable managed JSON: "+err.Error())
			continue
		}
		loaded[name] = document
	}

	// Identity first. Everything below is derived from the project's own id
	// and classification, so a repair that cannot read them has nothing to
	// reconcile against and must not guess.
	project := loaded["project.json"]
	if project == nil {
		plan.block(Overlay+"/project.json",
			"missing project identity; cannot safely reconstruct it")
	} else {
		projectID, hasID := project["project_id"].(string)
		classification, hasClassification := project["classification"].(string)
		if !hasID || !hasClassification || projectID == "" || classification == "" {
			plan.block(Overlay+"/project.json", "missing project_id or classification")
		}
	}

	var profile *orderedObject
	var documents []overlayDocument
	var routing map[string]any
	if len(plan.blockers) == 0 && project != nil {
		profile, documents, routing = r.planRepairArtifacts(plan, filesystem.Root(), project)
	}

	r.planAgentsMarkdown(plan, filesystem)

	var catalog map[string]any
	var expectedLock *orderedObject
	if len(plan.blockers) == 0 && project != nil {
		r.planOverlayDocuments(plan, filesystem, documents)
		expectedLock = r.planVersionLock(plan, filesystem, project, profile, routing)
		catalog = r.planWrappers(plan, filesystem, request.Runner, project, profile)
	}

	if len(plan.blockers) > 0 {
		return repairReport("blocked", filesystem.Root(), false, plan), 1, nil
	}
	if !request.Apply {
		status := "current"
		if len(plan.actions) > 0 {
			status = "repair-available"
		}
		return repairReport(status, filesystem.Root(), false, plan), 0, nil
	}

	if err := r.applyRepair(filesystem, plan, request.Runner,
		project, profile, documents, expectedLock, catalog); err != nil {
		plan.blockers = nil
		plan.block(Overlay, "secure repair failed: "+err.Error())
		return repairReport("blocked", filesystem.Root(), false, plan), 1, nil
	}
	status := "current"
	if len(plan.actions) > 0 {
		status = "repaired"
	}
	return repairReport(status, filesystem.Root(), len(plan.actions) > 0, plan), 0, nil
}

// planRepairArtifacts rebuilds what init would produce now, and blocks if the
// provider profile has moved since this project was initialised.
func (r *Registry) planRepairArtifacts(
	plan *repairPlan, root string, project map[string]any,
) (*orderedObject, []overlayDocument, map[string]any) {
	profileID := ""
	if value, present := project["profile"]; present && value != nil {
		text, ok := value.(string)
		if !ok {
			plan.block(Overlay+"/project.json", "profile must be a string or null")
			return nil, nil, nil
		}
		profileID = text
	}
	extensions, err := repairExtensionList(project["extensions"])
	if err != nil {
		plan.block(Overlay+"/project.json", "extensions must be a string list")
		return nil, nil, nil
	}

	projectID, _ := project["project_id"].(string)
	classification, _ := project["classification"].(string)
	profile, documents, routing, err := r.initializationArtifacts(
		root, profileID, extensions, projectID, classification, DetectRepository(root))
	if err != nil {
		plan.block(Overlay, "cannot load current provider/profile: "+err.Error())
		return nil, nil, nil
	}

	// A changed profile is a blocker, not something to reconcile. Repair could
	// regenerate the routing to match, and that would silently re-route work a
	// project has already made decisions about.
	digest, err := Fingerprint(profile)
	if err != nil {
		plan.block(Overlay, "cannot load current provider/profile: "+err.Error())
		return nil, nil, nil
	}
	if project["profile_digest"] != digest {
		plan.block(Overlay+"/project.json",
			"provider profile has changed; review and migrate project decisions explicitly")
	}
	return profile, documents, routing
}

func repairExtensionList(value any) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("extensions must be a string list")
	}
	extensions := make([]string, 0, len(items))
	for _, raw := range items {
		text, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("extensions must be a string list")
		}
		extensions = append(extensions, text)
	}
	return extensions, nil
}

// planAgentsMarkdown decides what to do with the managed block.
func (r *Registry) planAgentsMarkdown(plan *repairPlan, filesystem *RepairFilesystem) {
	state, err := filesystem.FileState("AGENTS.md")
	if err != nil {
		plan.block("AGENTS.md", "unsafe or unreadable managed file: "+err.Error())
		return
	}
	if state != "regular" {
		plan.act("AGENTS.md", "create_managed_block")
		return
	}
	text, err := filesystem.ReadText("AGENTS.md")
	if err != nil {
		plan.block("AGENTS.md", "unsafe or unreadable managed file: "+err.Error())
		return
	}
	// More than one block, or a start without an end, means the file has been
	// edited in a way this cannot reason about -- and rewriting it would
	// delete whatever the project put between or after the markers.
	starts := strings.Count(text, managedBlockStart)
	ends := strings.Count(text, managedBlockEnd)
	if starts != ends || starts > 1 {
		plan.block("AGENTS.md", "incomplete or ambiguous Agentic SDLC managed block")
		return
	}
	rendered, err := RenderAgentsMarkdown(text)
	if err != nil {
		plan.block("AGENTS.md", "incomplete or ambiguous Agentic SDLC managed block")
		return
	}
	if rendered != text {
		plan.act("AGENTS.md", "refresh_managed_block")
	}
}

// planOverlayDocuments marks each managed document as protected or missing.
func (r *Registry) planOverlayDocuments(
	plan *repairPlan, filesystem *RepairFilesystem, documents []overlayDocument,
) {
	for _, document := range documents {
		state, err := filesystem.FileState(Overlay, document.name)
		if err != nil {
			plan.block(Overlay+"/"+document.name, "unsafe managed artifact: "+err.Error())
			continue
		}
		if state == "regular" {
			plan.keep(Overlay + "/" + document.name)
			continue
		}
		plan.act(Overlay+"/"+document.name, "recreate_missing_baseline")
	}
}

// planVersionLock compares the recorded lock with what this kernel would write.
func (r *Registry) planVersionLock(
	plan *repairPlan, filesystem *RepairFilesystem,
	project map[string]any, profile *orderedObject, routing map[string]any,
) *orderedObject {
	profileID := ""
	if text, ok := project["profile"].(string); ok {
		profileID = text
	}
	expected, err := r.versionLock(profileID, profile, routing)
	if err != nil {
		plan.block(Overlay+"/version.lock", "unsafe or unreadable lock: "+err.Error())
		return nil
	}

	state, err := filesystem.FileState(Overlay, "version.lock")
	if err != nil {
		plan.block(Overlay+"/version.lock", "unsafe or unreadable lock: "+err.Error())
		return expected
	}
	if state != "regular" {
		plan.act(Overlay+"/version.lock", "recreate_missing_lock")
		return expected
	}
	text, err := filesystem.ReadText(Overlay, "version.lock")
	if err != nil {
		plan.block(Overlay+"/version.lock", "unsafe or unreadable lock: "+err.Error())
		return expected
	}
	lock, err := decodeJSONObject([]byte(text))
	if err != nil {
		plan.block(Overlay+"/version.lock", "unsafe or unreadable lock: "+err.Error())
		return expected
	}

	// Provenance moving is a blocker; the kernel's own version moving is an
	// upgrade. The lock exists to notice the first of those.
	var drift []string
	for _, key := range lockProvenanceKeys {
		if !sameJSONValue(lock[key], expected.values[key]) {
			drift = append(drift, key)
		}
	}
	if len(drift) > 0 {
		plan.block(Overlay+"/version.lock",
			"lock provenance has changed ("+strings.Join(drift, ",")+
				"); review and migrate explicitly")
		return expected
	}
	var changed []string
	for _, key := range lockUpgradeKeys {
		if !sameJSONValue(lock[key], expected.values[key]) {
			changed = append(changed, key)
		}
	}
	if len(changed) > 0 {
		plan.act(Overlay+"/version.lock", "upgrade_lock:"+strings.Join(changed, ","))
		return expected
	}
	plan.keep(Overlay + "/version.lock")
	return expected
}

// planWrappers marks each runner wrapper as protected or missing.
func (r *Registry) planWrappers(
	plan *repairPlan, filesystem *RepairFilesystem, runner string,
	project map[string]any, profile *orderedObject,
) map[string]any {
	if project["profile"] == nil {
		return nil
	}
	catalog, err := r.LoadAgentCatalog()
	if err != nil {
		plan.block(Overlay, "cannot load current provider/profile: "+err.Error())
		return nil
	}
	for _, target := range wrapperTargets(runner) {
		for _, raw := range listOf(profile.values["agents"]) {
			agentID, ok := raw.(string)
			if !ok {
				continue
			}
			if _, known := catalog[agentID]; !known {
				continue
			}
			parts := wrapperParts(target, agentID)
			state, err := filesystem.FileState(parts...)
			if err != nil {
				plan.block(strings.Join(parts, "/"), "unsafe wrapper path: "+err.Error())
				continue
			}
			if state == "regular" {
				plan.keep(strings.Join(parts, "/"))
				continue
			}
			plan.act(strings.Join(parts, "/"), "recreate_missing_wrapper")
		}
	}
	return catalog
}

type wrapperTarget struct {
	runner    string
	extension string
}

func wrapperTargets(runner string) []wrapperTarget {
	if runner == "" {
		runner = "both"
	}
	var targets []wrapperTarget
	if runner == "codex" || runner == "both" {
		targets = append(targets, wrapperTarget{"codex", "toml"})
	}
	if runner == "claude" || runner == "both" {
		targets = append(targets, wrapperTarget{"claude", "md"})
	}
	return targets
}

func wrapperParts(target wrapperTarget, agentID string) []string {
	return []string{"." + target.runner, "agents", agentID + "." + target.extension}
}

// applyRepair performs exactly the writes the plan named, and nothing else.
func (r *Registry) applyRepair(
	filesystem *RepairFilesystem, plan *repairPlan, runner string,
	project map[string]any, profile *orderedObject, documents []overlayDocument,
	expectedLock *orderedObject, catalog map[string]any,
) error {
	paths := plan.actionPaths()

	for _, document := range documents {
		if !paths[Overlay+"/"+document.name] {
			continue
		}
		// overwrite=false is the second line, not the first: the plan already
		// excluded every document that exists. It earns its place only in the
		// race -- a file appearing between planning and this write -- where it
		// makes repair lose rather than clobber. That behaviour is proven in
		// repairfs_test.go; removing the flag here changes nothing a test can
		// see, which is why it is explained rather than asserted.
		if _, err := filesystem.WriteText(
			[]string{Overlay, document.name}, RenderIndented(document.value), false); err != nil {
			return err
		}
	}

	if paths[Overlay+"/version.lock"] {
		if err := r.applyVersionLock(filesystem, expectedLock); err != nil {
			return err
		}
	}

	if paths["AGENTS.md"] {
		current := ""
		state, err := filesystem.FileState("AGENTS.md")
		if err != nil {
			return err
		}
		if state == "regular" {
			if current, err = filesystem.ReadText("AGENTS.md"); err != nil {
				return err
			}
		}
		rendered, err := RenderAgentsMarkdown(current)
		if err != nil {
			return err
		}
		// The only overwrite repair performs, and it is bounded to the block
		// between the two markers -- everything else in the file is carried
		// through by the renderer.
		if _, err := filesystem.WriteText([]string{"AGENTS.md"}, rendered, true); err != nil {
			return err
		}
	}

	if project["profile"] == nil || profile == nil {
		return nil
	}
	for _, target := range wrapperTargets(runner) {
		for _, raw := range listOf(profile.values["agents"]) {
			agentID, ok := raw.(string)
			if !ok {
				continue
			}
			metadata, known := catalog[agentID].(map[string]any)
			parts := wrapperParts(target, agentID)
			if !known || !paths[strings.Join(parts, "/")] {
				continue
			}
			reviewer := metadata["kind"] == "reviewer"
			body := agentWrapperBody(agentID, reviewer, metadata, profile)
			content := repairClaudeWrapper(agentID, reviewer, metadata, body)
			if target.runner == "codex" {
				content = codexWrapper(agentID, reviewer, metadata, body)
			}
			if _, err := filesystem.WriteText(parts, content, false); err != nil {
				return err
			}
		}
	}
	return nil
}

// applyVersionLock writes the lock, re-checking provenance immediately before
// the write.
//
// Re-checked because planning and writing are separate moments: a lock whose
// provenance changed in between is one somebody else is editing, and upgrading
// it would overwrite their change with a version stamp.
func (r *Registry) applyVersionLock(
	filesystem *RepairFilesystem, expected *orderedObject,
) error {
	state, err := filesystem.FileState(Overlay, "version.lock")
	if err != nil {
		return err
	}
	if state != "regular" {
		_, err := filesystem.WriteText(
			[]string{Overlay, "version.lock"}, RenderIndented(expected), false)
		return err
	}

	text, err := filesystem.ReadText(Overlay, "version.lock")
	if err != nil {
		return err
	}
	decoded, err := DecodeOrdered([]byte(text))
	if err != nil {
		return err
	}
	lock, ok := decoded.(*orderedObject)
	if !ok {
		return fmt.Errorf("version.lock must contain a JSON object")
	}
	for _, key := range lockProvenanceKeys {
		if !sameJSONValue(lock.values[key], expected.values[key]) {
			return fmt.Errorf("version.lock provenance changed during repair planning")
		}
	}
	// Only the kernel's own fields move; anything else the project keeps.
	for _, key := range lockUpgradeKeys {
		lock.set(key, expected.values[key])
	}
	_, err = filesystem.WriteText([]string{Overlay, "version.lock"}, RenderIndented(lock), true)
	return err
}

func repairReport(status, root string, mutation bool, plan *repairPlan) *orderedObject {
	actions := []any{}
	for _, action := range plan.actions {
		actions = append(actions, ordered("path", action.path, "action", action.detail))
	}
	blockers := []any{}
	for _, blocker := range plan.blockers {
		blockers = append(blockers, ordered("path", blocker.path, "reason", blocker.detail))
	}
	protected := []any{}
	for _, path := range plan.protected {
		protected = append(protected, path)
	}
	return ordered(
		"status", status,
		"mutation", mutation,
		"root", root,
		"actions", actions,
		"protected", protected,
		"blockers", blockers,
	)
}

// sameJSONValue compares two values as the JSON documents they represent.
//
// Not reflect.DeepEqual: one side has been through encoding/json and holds
// float64(2), the other was built in Go and holds int(2). They are the same
// JSON number and DeepEqual says otherwise -- which reported every project's
// version lock as needing an upgrade it did not need.
func sameJSONValue(left, right any) bool {
	leftBytes, leftErr := canonicaljson.Marshal(left)
	rightBytes, rightErr := canonicaljson.Marshal(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return string(leftBytes) == string(rightBytes)
}

// repairClaudeWrapper renders a Claude wrapper the way `repair` does, which is
// one blank line different from the way `init` does.
//
// That difference is the Python kernel's, not this port's: its `init` joins
// the frontmatter and appends the body, while its `repair` builds one list
// with an extra empty element between them. So a project's wrapper content
// depends on which command happened to create it.
//
// Reproduced rather than corrected, deliberately. Fixing it here would make
// every repair differ from the kernel this is being checked against, which
// hides real divergences behind an intentional one. It is recorded as a
// follow-up to fix in one place once the Python kernel is gone.
func repairClaudeWrapper(
	agentID string, reviewer bool, metadata map[string]any, body string,
) string {
	tools := "Read, Grep, Glob, Bash, Edit, Write"
	if reviewer {
		tools = "Read, Grep, Glob, Bash"
	}
	return strings.Join([]string{
		"---",
		"name: " + agentID,
		"description: " + wrapperDescription(metadata),
		"tools: " + tools,
		"---",
		"",
		body,
		"",
	}, "\n")
}
