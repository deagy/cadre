package selector

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// SchemaVersion is the plan's declared contract version.
//
// selection.schema.json is closed (additionalProperties: false) and is
// vendored away from the producer, so a consumer's pinned copy rejects a plan
// carrying a property it has never seen. RUNBOOK.md's rule is therefore that
// *any* change to the emitted field set bumps this -- a pinned consumer must
// break loudly on the version rather than quietly on an unknown key, or worse
// quietly exec an argv it cannot run.
const SchemaVersion = 8

// StandaloneReason is emitted when no lifecycle contract is available.
const StandaloneReason = "Agentic SDLC executable not found; team dispatch is unaffected."

// PlanInput is what build_dispatch_plan calls input_data.
type PlanInput struct {
	Task              string
	TaskID            string
	RepositoryRoot    string
	Base              string
	ChangedFileSource string
	ChangedFiles      []string
	Classification    string
	Sources           []string
	// Top is nil when the caller expressed no preference; see KnowledgeInput.
	Top *int
}

// PlanOptions carries what the plan needs from its environment.
type PlanOptions struct {
	Catalog      []string
	Gates        []LifecycleGate
	ContractVer  int
	RosterRoot   string
	KnowledgeCLI string
	Provenance   map[string]any
}

// BuildDispatchPlan ports build_dispatch_plan.
//
// The assembly order is the port's contract: agents accumulate from routes,
// then risks, then cross-stack, then change intake, then gate agents, and only
// then are ordered and de-duplicated across groups. Reordering any of that
// changes which agent lands in which group.
func BuildDispatchPlan(config map[string]any, input PlanInput, options PlanOptions) (map[string]any, error) {
	gates := options.Gates

	matchedRoutes := MatchRoutes(config, input.Task, input.ChangedFiles)
	matchedPacks := MatchContextPacks(config, input.Task, input.ChangedFiles)
	matchedRisks := ClassifyRisks(config, input.Task, input.ChangedFiles)

	var primary, reviewers, support []string
	for _, match := range append(append([]Match{}, matchedRoutes...), matchedRisks...) {
		primary = append(primary, stringSlice(match.Rule["primary"])...)
		reviewers = append(reviewers, stringSlice(match.Rule["reviewers"])...)
		support = append(support, stringSlice(match.Rule["support"])...)
	}
	support = append(support, ApplyCrossStack(config, matchedRoutes)...)

	changeIntake, _ := config["change_intake"].(map[string]any)
	intakeMatched := MatchesChangeIntake(config, input.Task)
	if intakeMatched && changeIntake != nil {
		support = append(support, stringSlice(changeIntake["agents"])...)
	}

	var configuredGateIDs []string
	for _, match := range append(append([]Match{}, matchedRoutes...), matchedRisks...) {
		configuredGateIDs = append(configuredGateIDs, stringSlice(match.Rule["quality_gates"])...)
	}
	if intakeMatched && changeIntake != nil {
		configuredGateIDs = append(configuredGateIDs, stringSlice(changeIntake["quality_gates"])...)
	}
	ignoredGates := stringSlice(config["ignored_gates"])
	gateAgents, err := GateAgents(configuredGateIDs, ignoredGates, gates,
		stringSlice(config["default_gate_review_agents"]))
	if err != nil {
		return nil, err
	}
	support = append(support, gateAgents...)

	groups := AgentGroups{
		Primary:   Ordered(primary, options.Catalog),
		Reviewers: Ordered(reviewers, options.Catalog),
		Support:   Ordered(support, options.Catalog),
	}
	groups.Reviewers = subtract(groups.Reviewers, groups.Primary)
	groups.Support = subtract(groups.Support, groups.Primary, groups.Reviewers)
	if err := ValidateAgents(groups, options.Catalog); err != nil {
		return nil, err
	}

	selectedAgents := Ordered(
		append(append(append([]string{}, groups.Primary...), groups.Reviewers...), groups.Support...),
		options.Catalog)
	teams := BuildTeams(config, matchedRoutes, selectedAgents, input.Task)

	var riskIDs []string
	for _, risk := range matchedRisks {
		riskIDs = append(riskIDs, risk.ID)
	}

	taskID := input.TaskID
	if taskID == "" {
		fingerprint := input.Task + "\n" + strings.Join(input.ChangedFiles, "\n")
		sum := sha256.Sum256([]byte(fingerprint))
		taskID = "local-" + hex.EncodeToString(sum[:])[:12]
	}

	requiredGates, err := BuildQualityGates(matchedRoutes, matchedRisks, gates)
	if err != nil {
		return nil, err
	}
	existing := map[string]bool{}
	for _, gate := range requiredGates {
		existing[gate.ID] = true
	}
	if intakeMatched && changeIntake != nil {
		intakeGateIDs := stringSlice(changeIntake["quality_gates"])
		byID := map[string]LifecycleGate{}
		for _, gate := range gates {
			byID[gate.ID] = gate
		}
		if gates != nil {
			if err := rejectUnknownGates(intakeGateIDs, GateOrder(gates),
				"Change intake references unknown lifecycle gates"); err != nil {
				return nil, err
			}
		}
		for _, gateID := range intakeGateIDs {
			if existing[gateID] {
				continue
			}
			reason := gateDetailOmitted
			if gates != nil {
				gate := byID[gateID]
				name := gate.Name
				if name == "" {
					name = gateID
				}
				phase := gate.Phase
				if phase == "" {
					phase = "unspecified"
				}
				reason = fmt.Sprintf("%s lifecycle gate (%s phase).", name, phase)
			}
			requiredGates = append(requiredGates, QualityGate{
				ID: gateID, Required: true, Reason: reason,
				ContributingRoutes: []string{"change-intake"},
			})
			existing[gateID] = true
		}
	}
	if gates != nil {
		position := indexOf(GateOrder(gates))
		sort.SliceStable(requiredGates, func(i, j int) bool {
			return position[requiredGates[i].ID] < position[requiredGates[j].ID]
		})
	}

	var configuredForSequence []string
	for _, gate := range requiredGates {
		configuredForSequence = append(configuredForSequence, gate.ID)
	}
	effective, ignoredQualityGates, err := GateSequence(configuredForSequence, ignoredGates, gates)
	if err != nil {
		return nil, err
	}
	byGateID := map[string]QualityGate{}
	for _, gate := range requiredGates {
		byGateID[gate.ID] = gate
	}
	rebuilt := make([]QualityGate, 0, len(effective))
	for _, gateID := range effective {
		if gate, ok := byGateID[gateID]; ok {
			rebuilt = append(rebuilt, gate)
			continue
		}
		rebuilt = append(rebuilt, QualityGate{
			ID: gateID, Required: true,
			Reason:             "Required by the standalone lifecycle gate sequence.",
			ContributingRoutes: []string{"lifecycle-sequence"},
		})
	}

	lifecycleTracking := map[string]any{"status": "standalone", "reason": StandaloneReason}
	if gates != nil {
		lifecycleTracking = map[string]any{"status": "integrated"}
	}

	knowledge, err := BuildKnowledgeContext(mapOf(config["knowledge_focus"]), selectedAgents, KnowledgeInput{
		Task: input.Task, TaskID: taskID, Classification: input.Classification,
		Sources: input.Sources, Top: input.Top, KnowledgeCLI: options.KnowledgeCLI,
	})
	if err != nil {
		return nil, err
	}
	packs, err := BuildContextPacks(matchedPacks, input.Classification, options.RosterRoot)
	if err != nil {
		return nil, err
	}

	status := "needs-triage"
	if len(selectedAgents) > 0 {
		status = "ready"
	}
	generatedAt := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")

	plan := map[string]any{
		"schema_version": SchemaVersion,
		"task_id":        taskID,
		"generated_at":   generatedAt,
		"status":         status,
		"workflow":       SelectWorkflow(matchedRoutes, riskIDs, len(selectedAgents) > 0),
		"inputs": map[string]any{
			"task":                input.Task,
			"repository_root":     input.RepositoryRoot,
			"base":                nullableString(input.Base),
			"changed_file_source": input.ChangedFileSource,
			"changed_files":       orEmpty(input.ChangedFiles),
			"classification":      nullableString(input.Classification),
			"source_filter":       orEmpty(input.Sources),
		},
		"matched_routes":         reasonEntries(matchedRoutes),
		"matched_risks":          reasonEntries(matchedRisks),
		"context_packs":          packs,
		"agents":                 groups,
		"dispatch_disposition":   BuildDispatchDisposition(groups),
		"teams":                  teams,
		"lifecycle_tracking":     lifecycleTracking,
		"required_quality_gates": rebuilt,
		"ignored_quality_gates":  ignoredQualityGates,
		"human_gates":            BuildHumanGates(matchedRisks),
		"knowledge_context":      knowledge,
	}
	// Optional and omitted when empty -- but note it IS part of the hashed
	// payload, deliberately: matched_routes emits only id + reasons and never
	// the shape, so a route flipping between workflow_shape "unclassified" and
	// omitting the field would otherwise produce a byte-identical plan.
	if undeclared := UndeclaredWorkflowShapeRoutes(matchedRoutes); len(undeclared) > 0 {
		plan["undeclared_workflow_shape_routes"] = undeclared
	}
	if options.Provenance != nil {
		plan["provenance"] = options.Provenance
	}

	fingerprint, err := DispatchFingerprint(plan)
	if err != nil {
		return nil, err
	}
	plan["dispatch_fingerprint"] = fingerprint
	return plan, nil
}

// reasonEntries emits a match's id and reasons, which is all the plan carries
// for a matched route or risk.
func reasonEntries(matches []Match) []map[string]any {
	entries := make([]map[string]any, 0, len(matches))
	for _, match := range matches {
		entries = append(entries, map[string]any{
			"id": match.ID,
			"reasons": map[string]any{
				"keywords":       orEmpty(match.Reasons.Keywords),
				"keyword_groups": orEmptyGroups(match.Reasons.KeywordGroups),
				"paths":          orEmptyPaths(match.Reasons.Paths),
			},
		})
	}
	return entries
}

func subtract(values []string, exclusions ...[]string) []string {
	excluded := map[string]bool{}
	for _, group := range exclusions {
		for _, value := range group {
			excluded[value] = true
		}
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if !excluded[value] {
			out = append(out, value)
		}
	}
	return out
}

// nullableString emits JSON null for an absent optional input, matching
// Python's `.get(key)` returning None rather than "".
func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func mapOf(value any) map[string]any {
	out, _ := value.(map[string]any)
	return out
}

func orEmptyGroups(groups [][]string) [][]string {
	if groups == nil {
		return [][]string{}
	}
	return groups
}

func orEmptyPaths(paths []PathMatch) []PathMatch {
	if paths == nil {
		return []PathMatch{}
	}
	return paths
}

// PlanKeyOrder is the order build_dispatch_plan inserts keys, and therefore
// the order `json.dumps(plan, indent=2)` emits them -- that call passes no
// sort_keys, so Python's dict insertion order is the wire order.
//
// Go maps have no order at all, so reproducing the emitted document needs
// this written down. Note this is only for *output*: the fingerprint's
// canonical form sorts keys instead, and the two must not be conflated.
var PlanKeyOrder = []string{
	"schema_version",
	"task_id",
	"generated_at",
	"status",
	"workflow",
	"inputs",
	"matched_routes",
	"matched_risks",
	"undeclared_workflow_shape_routes",
	"context_packs",
	"agents",
	"dispatch_disposition",
	"teams",
	"lifecycle_tracking",
	"required_quality_gates",
	"ignored_quality_gates",
	"human_gates",
	"knowledge_context",
	"provenance",
	"dispatch_fingerprint",
}

// InputKeyOrder is the same, for the nested `inputs` object.
var InputKeyOrder = []string{
	"task", "repository_root", "base", "changed_file_source",
	"changed_files", "classification", "source_filter",
}

// RenderPlanJSON emits the plan the way select_agents.py does:
// json.dumps(plan, indent=2, ensure_ascii=False) plus a trailing newline.
//
// ensure_ascii is False here and True for the fingerprint's canonical form.
// That asymmetry is real and load-bearing: the wire document carries raw
// UTF-8 while the hashed payload escapes it, so a single shared encoder
// would necessarily get one of the two wrong.
func RenderPlanJSON(plan map[string]any) ([]byte, error) {
	normalized, err := normalizeForCanonical(plan)
	if err != nil {
		return nil, err
	}
	asMap, _ := normalized.(map[string]any)
	var builder strings.Builder
	if err := writeOrdered(&builder, asMap, PlanKeyOrder, 0); err != nil {
		return nil, err
	}
	builder.WriteByte('\n')
	return []byte(builder.String()), nil
}

func writeOrdered(builder *strings.Builder, value any, keyOrder []string, depth int) error {
	switch typed := value.(type) {
	case map[string]any:
		keys := orderedKeys(typed, keyOrder)
		if len(keys) == 0 {
			builder.WriteString("{}")
			return nil
		}
		builder.WriteString("{\n")
		for index, key := range keys {
			if index > 0 {
				builder.WriteString(",\n")
			}
			writeIndent(builder, depth+1)
			writeOutputString(builder, key)
			builder.WriteString(": ")
			nested := []string(nil)
			if key == "inputs" {
				nested = InputKeyOrder
			}
			if err := writeOrdered(builder, typed[key], nested, depth+1); err != nil {
				return err
			}
		}
		builder.WriteByte('\n')
		writeIndent(builder, depth)
		builder.WriteByte('}')
	case []any:
		if len(typed) == 0 {
			builder.WriteString("[]")
			return nil
		}
		builder.WriteString("[\n")
		for index, element := range typed {
			if index > 0 {
				builder.WriteString(",\n")
			}
			writeIndent(builder, depth+1)
			if err := writeOrdered(builder, element, nil, depth+1); err != nil {
				return err
			}
		}
		builder.WriteByte('\n')
		writeIndent(builder, depth)
		builder.WriteByte(']')
	default:
		return writeScalar(builder, value)
	}
	return nil
}

// orderedKeys returns preferred first, in order, then any remaining keys
// sorted -- so a key nobody listed still appears rather than vanishing.
func orderedKeys(value map[string]any, preferred []string) []string {
	seen := map[string]bool{}
	keys := make([]string, 0, len(value))
	for _, key := range preferred {
		if _, present := value[key]; present {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	var rest []string
	for key := range value {
		if !seen[key] {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	return append(keys, rest...)
}

func writeIndent(builder *strings.Builder, depth int) {
	builder.WriteString(strings.Repeat("  ", depth))
}

func writeScalar(builder *strings.Builder, value any) error {
	var nested strings.Builder
	if err := writeCanonical(&nested, value); err != nil {
		return err
	}
	if text, ok := value.(string); ok {
		// ensure_ascii=False on the wire: raw UTF-8, not \uXXXX.
		writeOutputString(builder, text)
		return nil
	}
	builder.WriteString(nested.String())
	return nil
}

// writeOutputString is writeCanonicalString with ensure_ascii=False: control
// characters are still escaped, but every printable rune is written as
// itself.
func writeOutputString(builder *strings.Builder, value string) {
	builder.WriteByte('"')
	for _, runeValue := range value {
		switch runeValue {
		case '"':
			builder.WriteString(`\"`)
		case '\\':
			builder.WriteString(`\\`)
		case '\b':
			builder.WriteString(`\b`)
		case '\f':
			builder.WriteString(`\f`)
		case '\n':
			builder.WriteString(`\n`)
		case '\r':
			builder.WriteString(`\r`)
		case '\t':
			builder.WriteString(`\t`)
		default:
			if runeValue < 0x20 {
				fmt.Fprintf(builder, `\u%04x`, runeValue)
				continue
			}
			builder.WriteRune(runeValue)
		}
	}
	builder.WriteByte('"')
}
