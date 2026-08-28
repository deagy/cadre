package orchestration

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/deagy/cadre/cli/internal/contextstore"
)

// Automatic capture of a dispatched child's final handoff into the context
// store.
//
// A port of dispatch_core.py's automatic_context_capture and
// context_tools.py's capture_final_handoff / _validated_final_handoff.
//
// Two rules govern everything here.
//
// First: stdout is never inspected. A handoff is something a runner states
// through the separate channel, in a declared envelope. Inferring one from
// arbitrary child output would make every transcript a candidate for
// automatic, permanent storage, and would let a child get text stored by
// printing something that looks structured.
//
// Second: identity, scope, classification, source and TTL are dispatch-owned.
// The child supplies none of them. It could otherwise write into another
// role's scope, label its own output below the session's ceiling, or place an
// entry in a source partition belonging to a different project.

const (
	// FinalHandoffSchemaVersion is the only envelope version accepted.
	FinalHandoffSchemaVersion = 1

	// MaxFinalHandoffBytes caps the stored envelope.
	MaxFinalHandoffBytes = 64 * 1024

	// MaxHandoffTextBytes caps any single free-text field. The handoff is a
	// structured result, not a place to put a transcript.
	MaxHandoffTextBytes = 4 * 1024

	// MaxHandoffListItems caps every list, so a bounded envelope cannot be
	// made unbounded by repetition.
	MaxHandoffListItems = 32
)

var (
	contextHandlePattern = regexp.MustCompile(`^ctx_[0-9a-f]{32}$`)
	windowsDrivePattern  = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

	finalHandoffKeys = map[string]bool{
		"summary": true, "disposition": true, "findings": true, "assumptions": true,
		"unresolved_questions": true, "next_action": true, "context_handles": true,
		"knowledge_steward_handoffs": true,
	}
	artifactKeys = map[string]bool{
		"id": true, "kind": true, "revision": true, "digest": true, "uri": true,
	}
	handoffDispositions = map[string]bool{
		"complete": true, "approve": true, "request-changes": true,
		"needs-information": true, "blocked": true,
	}
	// Mirrors every property of roster/shared/output-schemas/finding.schema.json,
	// which is the contract roles are told to emit. The two are held in step by
	// TestFindingKeysCoverTheFindingSchema; a finding conforming to the schema
	// used to be rejected here, silently discarding the whole envelope.
	findingKeys = map[string]fieldKind{
		"id": fieldText, "title": fieldText, "severity": fieldText,
		"status": fieldText, "summary": fieldText, "recommendation": fieldText,
		"evidence": fieldTextList, "affected_assets": fieldTextList,
		"control_mappings": fieldTextList,
		"owner":            fieldNullableText, "due_date": fieldNullableText,
		"exception_reference": fieldNullableText,
	}
	knowledgeHandoffKeys = map[string]fieldKind{
		"title": fieldText, "summary": fieldText, "evidence": fieldTextList,
		"origin": fieldText, "proposed_classification": fieldText,
		"source_scope": fieldText, "sensitivity_notes": fieldText,
		"conflicts_or_staleness": fieldText, "untrusted_instruction_risk": fieldText,
		"recommended_action": fieldText,
	}
)

// CaptureResult is what automatic capture reports back into the dispatch
// result. Never an exception: a capture that failed must not change whether
// the child completed.
type CaptureResult map[string]any

// AutomaticContextCapture stores an explicit final-handoff envelope.
//
// Returns "not_provided" when the child wrote nothing, "not_captured" with a
// reason when it wrote something unusable, and "captured" with the handle
// otherwise. The three are kept distinct because "the child said nothing" and
// "the child said something we refused" are different facts.
func AutomaticContextCapture(
	projectRoot string,
	result map[string]any,
	roleID, taskID, sessionID, parentClassification, classification string,
) CaptureResult {
	if reason, ok := result["final_handoff_capture_error"].(string); ok && reason != "" {
		return CaptureResult{"status": "not_captured", "reason": reason}
	}
	envelope, present := result["final_handoff"]
	if !present || envelope == nil {
		return CaptureResult{"status": "not_provided"}
	}

	captured, err := validateFinalHandoff(envelope)
	if err != nil {
		return CaptureResult{"status": "not_captured", "reason": err.Error()}
	}
	encoded, err := json.Marshal(captured)
	if err != nil {
		return CaptureResult{"status": "not_captured", "reason": "final_handoff must be JSON-serializable"}
	}
	if len(encoded) > MaxFinalHandoffBytes {
		return CaptureResult{"status": "not_captured",
			"reason": fmt.Sprintf("final_handoff exceeds the %d-byte capture cap", MaxFinalHandoffBytes)}
	}

	// The role is the already-resolved dispatch role, not an ambient
	// environment variable this server happens to carry: a capture attributed
	// to the wrong agent lands in the wrong scope.
	checked, err := checkedClassification(classification, parentClassification)
	if err != nil {
		return CaptureResult{"status": "not_captured", "reason": err.Error()}
	}
	if taskID == "" {
		return CaptureResult{"status": "not_captured",
			"reason": "no task identifier is available, so the entry would be unattributable"}
	}
	// Dispatch scope needs a dispatch identity. Without one the entry would
	// be readable by peers of no particular session.
	if sessionID == "" {
		return CaptureResult{"status": "not_captured",
			"reason": "scope 'dispatch' needs a dispatch identity: set a session id for this dispatch"}
	}

	source := DispatchSource(projectRoot)
	derived, _ := captured["derived_from"].([]string)

	cfg, done, err := openContextStore()
	if err != nil {
		return CaptureResult{"status": "not_captured", "reason": err.Error()}
	}
	defer done()
	db, err := contextstore.OpenStore(cfg.Database, true)
	if err != nil {
		return CaptureResult{"status": "not_captured",
			"reason": fmt.Sprintf("context store is unavailable: %s", err)}
	}
	defer func() { _ = db.Close() }()

	stored, err := contextstore.PutEntry(db, cfg, contextstore.PutOptions{
		Scope:          "dispatch",
		Classification: checked,
		Agent:          roleID,
		TaskID:         taskID,
		DispatchID:     sessionID,
		Label:          "final handoff: " + roleID,
		Source:         source,
		Content:        string(encoded),
		Tags:           []string{"automatic", "final-handoff"},
		DerivedFrom:    derived,
	})
	if err != nil {
		return CaptureResult{"status": "not_captured", "reason": err.Error()}
	}

	return CaptureResult{
		"status": "captured", "handle": stored.Handle,
		"expires_at": stored.ExpiresAt, "untrusted_inputs": stored.UntrustedInputs,
		"source": source,
	}
}

// validateFinalHandoff checks the envelope against the narrow protocol and
// returns the normalized object to store.
//
// Everything it accepts is bounded and identifier-shaped. The envelope is a
// machine result, and the fields it rejects -- unknown keys, unbounded lists,
// free text past the cap, artifact paths -- are the ones that would turn it
// into a general-purpose channel for storing whatever the child produced.
func validateFinalHandoff(envelope any) (map[string]any, error) {
	object, ok := envelope.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("final_handoff must be a structured object")
	}
	if object["kind"] != "cadre-final-handoff" || !isSchemaVersion(object["schema_version"]) {
		return nil, fmt.Errorf(
			"final_handoff must declare kind='cadre-final-handoff' and schema_version=%d",
			FinalHandoffSchemaVersion)
	}
	// Exactly these five keys. An unknown top-level field is a protocol the
	// child invented, and accepting it would store data nothing validated.
	expected := map[string]bool{
		"kind": true, "schema_version": true, "handoff": true,
		"artifacts": true, "derived_from": true,
	}
	for key := range object {
		if !expected[key] {
			return nil, fmt.Errorf("final_handoff has unsupported top-level fields")
		}
	}
	if len(object) != len(expected) {
		return nil, fmt.Errorf("final_handoff has unsupported top-level fields")
	}

	handoff, err := validateHandoffBody(object["handoff"])
	if err != nil {
		return nil, err
	}

	artifacts, err := validateArtifacts(object["artifacts"])
	if err != nil {
		return nil, err
	}

	derived, err := validateDerivedFrom(object["derived_from"])
	if err != nil {
		return nil, err
	}

	// Every handle the handoff cites must also appear in derived_from, which
	// is what the store records as provenance. A handle referenced but not
	// declared would leave the entry claiming a lineage the store never saw.
	declared := make(map[string]bool, len(derived))
	for _, reference := range derived {
		declared[reference] = true
	}
	if cited, ok := handoff["context_handles"].([]string); ok {
		for _, handle := range cited {
			if !declared[handle] {
				return nil, fmt.Errorf(
					"every final_handoff context handle must also be listed in derived_from")
			}
		}
	}

	return map[string]any{
		"kind":           "cadre-final-handoff",
		"schema_version": FinalHandoffSchemaVersion,
		"handoff":        handoff,
		"artifacts":      artifacts,
		"derived_from":   derived,
	}, nil
}

func isSchemaVersion(value any) bool {
	switch typed := value.(type) {
	case float64:
		return int(typed) == FinalHandoffSchemaVersion
	case int:
		return typed == FinalHandoffSchemaVersion
	}
	return false
}

func validateHandoffBody(value any) (map[string]any, error) {
	body, ok := value.(map[string]any)
	if !ok || len(body) == 0 {
		return nil, fmt.Errorf(
			"final_handoff.handoff must use only the documented structured handoff fields")
	}
	checked := map[string]any{}
	for field, item := range body {
		if !finalHandoffKeys[field] {
			return nil, fmt.Errorf(
				"final_handoff.handoff must use only the documented structured handoff fields")
		}
		switch field {
		case "disposition":
			text, _ := item.(string)
			if !handoffDispositions[text] {
				return nil, fmt.Errorf("final_handoff.handoff.disposition is invalid")
			}
			checked[field] = text
		case "summary", "next_action":
			text, err := shortText(item, "final_handoff.handoff."+field)
			if err != nil {
				return nil, err
			}
			checked[field] = text
		case "assumptions", "unresolved_questions":
			list, err := shortTextList(item, "final_handoff.handoff."+field, MaxHandoffListItems)
			if err != nil {
				return nil, err
			}
			checked[field] = list
		case "findings":
			entries, err := validateKeyedList(item, "final_handoff.handoff.findings", findingKeys, "finding")
			if err != nil {
				return nil, err
			}
			checked[field] = entries
		case "knowledge_steward_handoffs":
			entries, err := validateKeyedList(item,
				"final_handoff.handoff.knowledge_steward_handoffs", knowledgeHandoffKeys, "knowledge handoff")
			if err != nil {
				return nil, err
			}
			checked[field] = entries
		case "context_handles":
			handles, err := validateHandleList(item)
			if err != nil {
				return nil, err
			}
			checked[field] = handles
		}
	}
	return checked, nil
}

// fieldKind says how one field of a keyed-list entry is validated. A schema
// property that is nullable needs fieldNullableText: a coverage finding, which
// reports that no role's remit covered a defect, carries a null owner because
// there is no role to name.
type fieldKind int

const (
	fieldText fieldKind = iota
	fieldTextList
	fieldNullableText
)

func validateKeyedList(value any, field string, allowed map[string]fieldKind, entryName string) ([]map[string]any, error) {
	list, ok := value.([]any)
	if !ok || len(list) > MaxHandoffListItems {
		return nil, fmt.Errorf("%s must be a bounded list", field)
	}
	entries := make([]map[string]any, 0, len(list))
	for _, raw := range list {
		entry, ok := raw.(map[string]any)
		if !ok || len(entry) == 0 {
			return nil, fmt.Errorf("%s entries must use the structured %s fields only", field, entryName)
		}
		checked := map[string]any{}
		for key, item := range entry {
			kind, known := allowed[key]
			if !known {
				return nil, fmt.Errorf("%s entries must use the structured %s fields only", field, entryName)
			}
			if kind == fieldTextList {
				list, err := shortTextList(item, entryName+"."+key, 16)
				if err != nil {
					return nil, err
				}
				checked[key] = list
				continue
			}
			if kind == fieldNullableText && item == nil {
				checked[key] = nil
				continue
			}
			text, err := shortText(item, entryName+"."+key)
			if err != nil {
				return nil, err
			}
			checked[key] = text
		}
		entries = append(entries, checked)
	}
	return entries, nil
}

func validateHandleList(value any) ([]string, error) {
	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("final_handoff.handoff.context_handles must contain context handles only")
	}
	handles := make([]string, 0, len(list))
	for _, raw := range list {
		handle, ok := raw.(string)
		if !ok || !contextHandlePattern.MatchString(handle) {
			return nil, fmt.Errorf("final_handoff.handoff.context_handles must contain context handles only")
		}
		handles = append(handles, handle)
	}
	return handles, nil
}

func validateArtifacts(value any) ([]map[string]any, error) {
	list, ok := value.([]any)
	if !ok || len(list) > 64 {
		return nil, fmt.Errorf("final_handoff.artifacts must be a manifest of at most 64 entries")
	}
	artifacts := make([]map[string]any, 0, len(list))
	for _, raw := range list {
		entry, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("final_handoff.artifacts must be identifier-only manifest entries")
		}
		identifier, _ := entry["id"].(string)
		if identifier == "" {
			return nil, fmt.Errorf("final_handoff.artifacts must be identifier-only manifest entries")
		}
		for key := range entry {
			if !artifactKeys[key] {
				return nil, fmt.Errorf("final_handoff.artifacts must be identifier-only manifest entries")
			}
		}
		checked := map[string]any{}
		for _, field := range []string{"id", "kind", "revision", "digest"} {
			if item, present := entry[field]; present {
				validated, err := artifactIdentifier(item, "final_handoff.artifacts."+field, false)
				if err != nil {
					return nil, err
				}
				checked[field] = validated
			}
		}
		if item, present := entry["uri"]; present {
			validated, err := artifactIdentifier(item, "final_handoff.artifacts.uri", true)
			if err != nil {
				return nil, err
			}
			checked["uri"] = validated
		}
		artifacts = append(artifacts, checked)
	}
	return artifacts, nil
}

// artifactIdentifier accepts a name, never a location this process could be
// made to read.
//
// A manifest records *which* artifacts exist; it is not an instruction to
// fetch them. Absolute paths, file:// URIs and query strings are refused
// because each is a way to name something outside the project, and a future
// consumer that resolved a manifest entry would then be resolving one the
// child chose.
func artifactIdentifier(value any, field string, isURI bool) (string, error) {
	text, err := shortText(value, field)
	if err != nil {
		return "", err
	}
	if strings.Contains(text, "?") || strings.HasPrefix(text, "/") ||
		strings.HasPrefix(text, "\\") || windowsDrivePattern.MatchString(text) {
		return "", fmt.Errorf("%s may not be an absolute path or query-bearing identifier", field)
	}
	parsed, parseErr := url.Parse(text)
	if parseErr == nil && strings.EqualFold(parsed.Scheme, "file") {
		return "", fmt.Errorf("%s may not use a file URI", field)
	}
	if isURI && parseErr == nil && parsed.Scheme != "" {
		if !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" {
			return "", fmt.Errorf(
				"%s may use only an https URI or a repository-relative identifier", field)
		}
	}
	return text, nil
}

func validateDerivedFrom(value any) ([]string, error) {
	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("final_handoff.derived_from must be a list of provenance references")
	}
	references := make([]string, 0, len(list))
	for _, raw := range list {
		reference, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("final_handoff.derived_from must be a list of provenance references")
		}
		// Either a context handle this store issued, or the marker that says
		// the material came from untrusted knowledge. Nothing else: a free
		// string here would be provenance the store cannot check.
		if !contextHandlePattern.MatchString(reference) &&
			!strings.HasPrefix(reference, "ks:untrusted:") {
			return nil, fmt.Errorf(
				"final_handoff.derived_from may contain only context handles or ks:untrusted markers")
		}
		references = append(references, reference)
	}
	return references, nil
}

func shortText(value any, field string) (string, error) {
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("%s must be a non-empty string", field)
	}
	// Bounded in bytes and in lines. The line limit matters independently:
	// a field within the byte cap but made of hundreds of short lines is a
	// transcript wearing a structured field's name.
	if len(text) > MaxHandoffTextBytes || strings.Count(text, "\n") > 40 {
		return "", fmt.Errorf("%s exceeds the bounded final-handoff text limit", field)
	}
	if !utf8.ValidString(text) {
		return "", fmt.Errorf("%s must be valid UTF-8", field)
	}
	return text, nil
}

func shortTextList(value any, field string, limit int) ([]string, error) {
	list, ok := value.([]any)
	if !ok || len(list) > limit {
		return nil, fmt.Errorf("%s must be a list of at most %d strings", field, limit)
	}
	texts := make([]string, 0, len(list))
	for _, raw := range list {
		text, err := shortText(raw, field)
		if err != nil {
			return nil, err
		}
		texts = append(texts, text)
	}
	return texts, nil
}
