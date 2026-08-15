package selector

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Render a dispatch plan as text for a person, for `cadre select --format text`.
//
// The JSON plan is the contract every downstream tool reads and stays the
// default. This renders the same plan decision-first, and is a pure function
// of the plan: it never re-runs selection, never reads routing.json, and
// never adds a fact the plan does not already contain. For a plan whose whole
// value is being deterministic, a formatter that recomputed anything could
// disagree with the JSON it claims to be showing.
//
// Every field is read defensively. A plan from an older schema_version, or
// one truncated by a failure, renders what it has rather than failing: a
// formatter is a poor place to discover a schema change, and an error here
// would hide the plan entirely.

const (
	textWidth = 78
	textLabel = 11
)

func fillText(text, initial, subsequent string) string {
	return textwrapFill(text, textWidth, initial, subsequent)
}

// wrapValues renders one labelled row with continuation lines aligned under
// the first value.
func wrapValues(label string, values []string, separator string) []string {
	if len(values) == 0 {
		return nil
	}
	indent := strings.Repeat(" ", textLabel+2)
	body := textwrapFill(strings.Join(values, separator), textWidth, "", indent)
	return []string{fmt.Sprintf("  %-*s%s", textLabel, label, body)}
}

func textSection(title string) []string {
	return []string{"", title}
}

// routeSummary is `id (why)` -- the why is what makes a surprising route
// reviewable. It mirrors the JSON's reasons block rather than re-deriving
// anything.
func routeSummary(match map[string]any) string {
	routeID := stringOr(match["id"], "?")
	reasons := objectOf(match["reasons"])

	var parts []string
	var keywords []string
	for _, keyword := range anyList(reasons["keywords"]) {
		keywords = append(keywords, toText(keyword))
	}
	for _, group := range anyList(reasons["keyword_groups"]) {
		for _, keyword := range anyList(group) {
			keywords = append(keywords, toText(keyword))
		}
	}
	if len(keywords) > 0 {
		unique := sortedUnique(keywords)
		shown := make([]string, 0, 3)
		for _, keyword := range unique[:min(3, len(unique))] {
			shown = append(shown, `"`+keyword+`"`)
		}
		text := strings.Join(shown, ", ")
		if len(unique) > 3 {
			text += fmt.Sprintf(", +%d more", len(unique)-3)
		}
		parts = append(parts, text)
	}

	var patterns []string
	for _, path := range anyList(reasons["paths"]) {
		object, ok := path.(map[string]any)
		if !ok {
			continue
		}
		pattern, ok := object["pattern"].(string)
		if !ok || pattern == "" || containsString(patterns, pattern) {
			continue
		}
		patterns = append(patterns, pattern)
	}
	if len(patterns) > 0 {
		text := strings.Join(patterns[:min(2, len(patterns))], ", ")
		if len(patterns) > 2 {
			text += fmt.Sprintf(", +%d more", len(patterns)-2)
		}
		parts = append(parts, text)
	}

	if len(parts) == 0 {
		return routeID
	}
	return routeID + " (" + strings.Join(parts, "; ") + ")"
}

// FormatPlanText returns the human-readable rendering of a plan,
// newline-terminated.
//
// The plan is round-tripped through JSON first. A plan assembled in-process
// carries native Go shapes -- []string for an agent list, a struct for a
// gate -- while one read back from stdout carries only []any and
// map[string]any. Reading both would mean every accessor here handling two
// representations, and the failure mode when one is missed is silent: an
// agent list typed []string reads as empty, and the plan renders "NO AGENTS
// SELECTED" for a fully staffed change.
func FormatPlanText(plan map[string]any) string {
	if normalized, err := normalizePlanForText(plan); err == nil {
		plan = normalized
	}
	var lines []string

	status := stringOr(plan["status"], "unknown")
	taskID := stringOr(plan["task_id"], "")
	if taskID == "" {
		taskID = "(no --task-id given)"
	}
	inputs := objectOf(plan["inputs"])

	lines = append(lines, fmt.Sprintf("%s  [%s]", taskID, status))
	if task := strings.TrimSpace(toText(inputs["task"])); task != "" {
		lines = append(lines, fillText(task, "  ", "  "))
	}

	lines = append(lines, textSection("PLAN")...)
	lines = append(lines, wrapValues("workflow", []string{stringOr(plan["workflow"], "unknown")}, ", ")...)
	disposition := objectOf(plan["dispatch_disposition"])
	if dispositionStatus := toText(disposition["status"]); dispositionStatus != "" {
		lines = append(lines, wrapValues("dispatch", []string{dispositionStatus}, ", ")...)
	}
	var changed []string
	for _, path := range anyList(inputs["changed_files"]) {
		changed = append(changed, toText(path))
	}
	if len(changed) > 0 {
		shown := append([]string{}, changed[:min(5, len(changed))]...)
		if len(changed) > 5 {
			shown = append(shown, fmt.Sprintf("(+%d more)", len(changed)-5))
		}
		lines = append(lines, wrapValues("files", shown, ", ")...)
	}

	// needs-triage is the case a JSON skim gets wrong: the plan is
	// structurally valid and every agent list is simply empty, which reads as
	// success. Say it in words, with the reason the plan already carries.
	agents := objectOf(plan["agents"])
	staffed := false
	for _, group := range []string{"primary", "reviewers", "support"} {
		if len(anyList(agents[group])) > 0 {
			staffed = true
		}
	}
	if !staffed {
		lines = append(lines, textSection("NO AGENTS SELECTED")...)
		reason := toText(disposition["reason"])
		if reason == "" {
			reason = "No route or risk rule matched this task."
		}
		lines = append(lines, fillText(reason, "  ", "  "))
		lines = append(lines, "")
		lines = append(lines, "  Re-run with --explain to see which routes came closest and why.")
	} else {
		lines = append(lines, textSection("AGENTS")...)
		for _, group := range []string{"primary", "reviewers", "support"} {
			var members []string
			for _, agent := range anyList(agents[group]) {
				members = append(members, toText(agent))
			}
			// "(none)" rather than a bare "-": an empty reviewers slot is
			// worth noticing, and a lone dash at the end of a line reads as a
			// hyphenated id that wrapped.
			if row := wrapValues(group, members, ", "); len(row) > 0 {
				lines = append(lines, row...)
			} else {
				lines = append(lines, fmt.Sprintf("  %-*s(none)", textLabel, group))
			}
		}
	}

	if teams := anyList(plan["teams"]); len(teams) > 0 {
		lines = append(lines, textSection("TEAMS")...)
		for _, raw := range teams {
			team := objectOf(raw)
			lines = append(lines, fmt.Sprintf("  %s (%s, %s)",
				stringOr(team["id"], "?"), stringOr(team["type"], "?"),
				stringOr(team["communication_mode"], "?")))
			var members []string
			for _, member := range anyList(team["members"]) {
				members = append(members, toText(member))
			}
			lines = append(lines, wrapValues("", members, ", ")...)
		}
	}

	matchedRoutes := anyList(plan["matched_routes"])
	matchedRisks := anyList(plan["matched_risks"])
	if len(matchedRoutes) > 0 || len(matchedRisks) > 0 {
		lines = append(lines, textSection("MATCHED")...)
		if len(matchedRoutes) > 0 {
			summaries := make([]string, 0, len(matchedRoutes))
			for _, match := range matchedRoutes {
				summaries = append(summaries, routeSummary(objectOf(match)))
			}
			lines = append(lines, wrapValues("routes", summaries, "; ")...)
		}
		if len(matchedRisks) > 0 {
			ids := make([]string, 0, len(matchedRisks))
			for _, risk := range matchedRisks {
				ids = append(ids, stringOr(objectOf(risk)["id"], "?"))
			}
			lines = append(lines, wrapValues("risks", ids, ", ")...)
		}
	}

	// Human gates are the one part of a plan that is never advisory -- they
	// name a decision no agent may make. They get their own block, with the
	// reason attached, rather than an id in a list.
	var humanGates []map[string]any
	for _, raw := range anyList(plan["human_gates"]) {
		gate := objectOf(raw)
		if required, present := gate["required"]; !present || required == true {
			humanGates = append(humanGates, gate)
		}
	}
	if len(humanGates) > 0 {
		lines = append(lines, textSection("HUMAN APPROVAL REQUIRED")...)
		for _, gate := range humanGates {
			lines = append(lines, "  "+stringOr(gate["id"], "?"))
			if reason := strings.TrimSpace(toText(gate["reason"])); reason != "" {
				lines = append(lines, fillText(reason, "    ", "    "))
			}
		}
	}

	var qualityGates []string
	for _, raw := range anyList(plan["required_quality_gates"]) {
		gate := objectOf(raw)
		if required, present := gate["required"]; !present || required == true {
			qualityGates = append(qualityGates, stringOr(gate["id"], "?"))
		}
	}
	if len(qualityGates) > 0 {
		lines = append(lines, textSection("QUALITY GATES")...)
		lines = append(lines, wrapValues("required", qualityGates, ", ")...)
	}

	var packs []string
	for _, raw := range anyList(plan["context_packs"]) {
		if object, ok := raw.(map[string]any); ok {
			if id, present := object["id"]; present {
				packs = append(packs, toText(id))
				continue
			}
			packs = append(packs, pythonRepr(object))
			continue
		}
		packs = append(packs, toText(raw))
	}
	if len(packs) > 0 {
		lines = append(lines, textSection("CONTEXT PACKS")...)
		lines = append(lines, wrapValues("packs", packs, ", ")...)
	}

	if fingerprint := toText(plan["dispatch_fingerprint"]); fingerprint != "" {
		lines = append(lines, "", "  fingerprint "+fingerprint)
	}

	return strings.Join(lines, "\n") + "\n"
}

// toText is Python's str() for the JSON values a plan carries, which is what
// the formatter applies to every field before printing it.
func toText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	default:
		return pythonRepr(value)
	}
}

func stringOr(value any, fallback string) string {
	if text := toText(value); text != "" {
		return text
	}
	return fallback
}

func sortedUnique(values []string) []string {
	seen := map[string]bool{}
	var unique []string
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		unique = append(unique, value)
	}
	sort.Strings(unique)
	return unique
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}

// normalizePlanForText converts a plan to the generic JSON shapes the
// formatter reads. It deliberately decodes numbers as float64 rather than
// json.Number, matching what probe_text_parity.py fed the formatter when it
// was verified against Python -- so the shapes measured are the shapes
// rendered.
func normalizePlanForText(plan map[string]any) (map[string]any, error) {
	encoded, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}
