// Package validate checks the gate-record invariants of a run record.
//
// This is the residual, gate-record-only slice of the legacy CLI's
// validate_repository. That function also cross-checks a project overlay's
// authorities/routing/dispatch-plan files, provider and kernel version locks,
// and GitHub-review approval policy -- none of which this engine has built, so
// porting them would validate a record against structures that do not exist
// and either always pass or always fail.
//
// What is here is everything meaningful from a lifecycle_gates array alone,
// plus the schema shape. Ported from
// engine/agentic_sdlc_langgraph/validate.py.
//
// Exit convention, mirroring the legacy CLI:
//
//	0  valid
//	1  a real defect
//	2  structurally valid, blocked on an unresolved decision
//
// 2 is only returned when there are no hard errors: a blocker is something a
// human still has to decide, not something that is wrong.
package validate

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"github.com/deagy/cadre/cli/internal/engine/contracts"
)

// AllGateIDs is the exact sequence a run record must carry.
var AllGateIDs = []string{"G1", "G2", "G3", "G4", "G5", "G6", "G7", "G8", "G9", "G10"}

// isValidDateTime reports whether a value is a timestamp carrying an offset.
//
// Hand-rolled rather than left to the schema's `format: date-time`. The Python
// records having verified that date-time is not registered as a format checker
// in its environment -- the optional validator dependency is not installed --
// so FormatChecker silently no-ops there and would not catch a malformed
// timestamp at all. Go's schema library behaves differently again, and a check
// whose strictness depends on which optional extras happen to be present is
// not a check.
//
// An offset is required. Python parsed and then rejected a naive timestamp for
// having no tzinfo; RFC3339 rejects it at parse, which is the same answer.
//
// A space separator is accepted the way Python's fromisoformat accepts one,
// even though RFC3339 wants a T, so a record the Python considered valid is
// not newly rejected here.
func isValidDateTime(value any) bool {
	text, isText := value.(string)
	if !isText {
		return false
	}
	normalised := text
	if !strings.Contains(normalised, "T") {
		normalised = strings.Replace(normalised, " ", "T", 1)
	}
	_, err := time.Parse(time.RFC3339, normalised)
	return err == nil
}

// RunRecord validates the gate-record invariants of a run record.
//
// gateContracts is an optional gate_id -> lifecycle contract mapping. It
// enables two checks that a record cannot answer on its own: that an approved
// gate declares every authority its contract expects, and that a human_only
// gate is exempt from needing agent-produced evidence. Passing nil skips both,
// which keeps this usable as a pure record-only check at the cost of not
// catching a dropped authority requirement, and of holding a human_only gate
// to a standard it cannot meet.
func RunRecord(record map[string]any, schema *jsonschema.Schema, gateContracts map[string]contracts.Gate) (int, []string) {
	var validationErrors []string
	var blockers []string

	if schema != nil {
		if err := schema.Validate(record); err != nil {
			var validation *jsonschema.ValidationError
			if errors.As(err, &validation) {
				collectSchemaErrors(validation, &validationErrors)
			} else {
				validationErrors = append(validationErrors, "schema: "+err.Error())
			}
		}
	}

	gateRecords := asSlice(record["lifecycle_gates"])

	ids := make([]string, 0, len(gateRecords))
	for _, entry := range gateRecords {
		ids = append(ids, asString(asMap(entry)["gate_id"]))
	}
	if strings.Join(ids, ",") != strings.Join(AllGateIDs, ",") {
		validationErrors = append(validationErrors, "lifecycle_gates must be exactly G1-G10 in order")
	}

	for index, entry := range asSlice(record["re_entry_history"]) {
		invalidation := asMap(entry)
		if !isValidDateTime(invalidation["invalidated_at"]) {
			validationErrors = append(validationErrors, fmt.Sprintf(
				"re_entry_history[%d].invalidated_at %s is not a valid date-time",
				index, render(invalidation["invalidated_at"])))
		}
	}

	invalidationStarted := false
	for index, entry := range gateRecords {
		gate := asMap(entry)
		gateID := asString(gate["gate_id"])
		if gateID == "" {
			gateID = fmt.Sprintf("<gate index %d>", index)
		}
		status := asString(gate["status"])

		preparers := map[string]bool{}
		for _, preparer := range asSlice(gate["preparers"]) {
			if identity := asMap(preparer); identity != nil {
				preparers[asString(identity["id"])] = true
			}
		}
		verifier := asMap(gate["independent_verifier"])

		checkGateTimestamps(gate, gateID, &validationErrors)

		// Meaningful whatever the gate's current status.
		if verifier != nil && preparers[asString(verifier["id"])] {
			validationErrors = append(validationErrors, gateID+": independent_verifier is also a preparer")
		}

		// Once one gate in sequence is invalidated, every later one must be.
		if invalidationStarted && status != "invalidated" {
			validationErrors = append(validationErrors,
				gateID+": downstream gate must be invalidated once an earlier gate is")
		}
		if status == "invalidated" {
			invalidationStarted = true
			if asString(gate["required_reentry_gate"]) == "" {
				validationErrors = append(validationErrors,
					gateID+": invalidated gate is missing required_reentry_gate")
			}
		}

		if status != "approved" {
			continue
		}

		// Cannot be approved while an earlier applicable gate is not.
		for _, prior := range gateRecords[:index] {
			priorGate := asMap(prior)
			if asString(priorGate["status"]) != "approved" && asString(priorGate["applicability"]) != "not-applicable" {
				validationErrors = append(validationErrors,
					gateID+": approved before all prerequisite gates were approved")
				break
			}
		}

		contract, hasContract := gateContracts[gateID]
		isHumanOnly := hasContract && contract.HumanOnly

		if asString(gate["applicability"]) != "applicable" {
			validationErrors = append(validationErrors,
				gateID+": approved gate must have applicability=='applicable'")
		}
		// A human_only gate has no bound agents by design: its evidence is the
		// human decision, not an agent-produced artifact.
		if !isHumanOnly && (len(asSlice(gate["evidence_refs"])) == 0 || len(asSlice(gate["artifact_bindings"])) == 0) {
			validationErrors = append(validationErrors,
				gateID+": approved gate must have non-empty evidence_refs and artifact_bindings")
		}

		requirementIDs := map[string]bool{}
		var unknownIDs []string
		for _, requirement := range asSlice(gate["authority_requirements"]) {
			declared := asMap(requirement)
			authorityID := asString(declared["authority_id"])
			if requirementIDs[authorityID] {
				validationErrors = append(validationErrors,
					fmt.Sprintf("%s: duplicate authority requirement %s", gateID, render(authorityID)))
			}
			requirementIDs[authorityID] = true
			if asString(declared["applicability"]) == "unknown" {
				unknownIDs = append(unknownIDs, authorityID)
			}
		}

		if gateContracts != nil {
			var missing []string
			for _, expected := range contract.AuthorityRequirements {
				if !requirementIDs[expected] {
					missing = append(missing, expected)
				}
			}
			if len(missing) > 0 {
				sort.Strings(missing)
				validationErrors = append(validationErrors,
					fmt.Sprintf("%s: missing authority requirements %v", gateID, missing))
			}
		}

		if len(unknownIDs) > 0 {
			// A blocker, not an error. The legacy CLI called this an error;
			// "an authority's applicability is still unresolved" is exactly
			// the structurally-valid-but-undecided case the two-list
			// convention exists to separate from a defect.
			sort.Strings(unknownIDs)
			blockers = append(blockers, fmt.Sprintf(
				"%s: approved with unresolved authority applicability for %v", gateID, unknownIDs))
		}

		// If a verifier is present its independence must be declared. Whether
		// a given gate's profile binds a reviewer at all is a gate_bindings
		// decision this record-only function cannot see, so a gate with no
		// verifier is not penalised for having none.
		if verifier != nil {
			declaration := asMap(gate["independence_declaration"])
			confirmed, _ := declaration["verifier_confirmed_not_preparer"].(bool)
			if !confirmed {
				validationErrors = append(validationErrors,
					gateID+": has an independent_verifier but lacks its independence declaration")
			}
		}

		// An approver must be neither a preparer nor the verifier.
		for i, entry := range asSlice(gate["human_approvals"]) {
			approval := asMap(entry)
			approver := asMap(approval["approver"])
			if approver == nil {
				continue
			}
			approverID := asString(approver["id"])
			if preparers[approverID] || (verifier != nil && approverID == asString(verifier["id"])) {
				validationErrors = append(validationErrors,
					fmt.Sprintf("%s: human_approvals[%d].approver is not independent", gateID, i))
			}
		}
	}

	if len(validationErrors) > 0 {
		return 1, validationErrors
	}
	if len(blockers) > 0 {
		return 2, blockers
	}
	return 0, []string{}
}

func checkGateTimestamps(gate map[string]any, gateID string, out *[]string) {
	if gate["decided_at"] != nil && !isValidDateTime(gate["decided_at"]) {
		*out = append(*out, fmt.Sprintf("%s: decided_at %s is not a valid date-time",
			gateID, render(gate["decided_at"])))
	}
	for i, entry := range asSlice(gate["human_approvals"]) {
		approval := asMap(entry)
		if approval["decided_at"] != nil && !isValidDateTime(approval["decided_at"]) {
			*out = append(*out, fmt.Sprintf("%s: human_approvals[%d].decided_at %s is not a valid date-time",
				gateID, i, render(approval["decided_at"])))
		}
	}
	for i, entry := range asSlice(gate["invalidation_history"]) {
		invalidation := asMap(entry)
		if !isValidDateTime(invalidation["invalidated_at"]) {
			*out = append(*out, fmt.Sprintf("%s: invalidation_history[%d].invalidated_at %s is not a valid date-time",
				gateID, i, render(invalidation["invalidated_at"])))
		}
	}
	for i, entry := range asSlice(gate["exceptions"]) {
		exception := asMap(entry)
		if !isValidDateTime(exception["expires_at"]) {
			*out = append(*out, fmt.Sprintf("%s: exceptions[%d].expires_at %s is not a valid date-time",
				gateID, i, render(exception["expires_at"])))
		}
	}
}

// collectSchemaErrors flattens the library's error tree to its leaves.
//
// An aggregate node carries causes and no independently useful message, so
// only leaves are reported -- the most specific sub-error, matching what the
// Python surfaced from iter_errors.
func collectSchemaErrors(validation *jsonschema.ValidationError, out *[]string) {
	if len(validation.Causes) == 0 {
		location := validation.InstanceLocation
		if location == "" {
			location = "<root>"
		} else {
			location = strings.ReplaceAll(strings.TrimPrefix(location, "/"), "/", ".")
		}
		*out = append(*out, fmt.Sprintf("schema %s: %s", location, validation.Message))
		return
	}
	for _, cause := range validation.Causes {
		collectSchemaErrors(cause, out)
	}
}

// render formats a value the way Python's !r does for the common cases, so a
// diagnostic naming a bad timestamp still quotes it.
func render(value any) string {
	if text, isText := value.(string); isText {
		return "'" + text + "'"
	}
	if value == nil {
		return "None"
	}
	return fmt.Sprintf("%v", value)
}

func asSlice(value any) []any {
	if slice, ok := value.([]any); ok {
		return slice
	}
	return nil
}

func asMap(value any) map[string]any {
	if mapped, ok := value.(map[string]any); ok {
		return mapped
	}
	return nil
}

func asString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}
