package knowledge

// Disposition-history export/import: the half of the durability path the
// frontmatter cannot carry.
//
// A record's frontmatter holds its *current* disposition. Earlier ones cannot
// go there -- the frontmatter dialect is deliberately one level deep and holds
// no list of mappings -- so an export writes them beside the record, in an
// `<id>.history.json` sidecar. Without this file, importing such an export
// read the `.md` files and silently dropped every sidecar, so a store moved
// between machines arrived with its records intact, its audit trail gone, and
// no sign that anything was missing.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// StagedHistoryKeys is the exact shape StagedHistory returns and an export
// writes to an `<id>.history.json` sidecar. Nothing else is accepted: an entry
// carrying an unknown key is either a newer export format or a hand-edit, and
// both should fail loudly rather than be stored with the surplus dropped.
var StagedHistoryKeys = []string{
	"sequence",
	"action",
	"reason",
	"classification_used",
	"diverged_from_proposal",
	"decided_by",
	"decided_at",
}

// ValidateStagedHistory returns findings against one record's exported
// disposition history, or nil.
//
// Separate from PutStagedHistory so a batch importer can validate every
// record's history *before* writing any of them -- the same
// validate-then-write discipline PutStagedRecord follows, for the same reason:
// a half-restored audit trail is worse than a refused import, because nothing
// about it looks refused.
//
// The last entry must agree with the record's own `disposition`, and the
// record's `status` must equal that entry's action. This is not redundancy for
// its own sake: DispositionStagedRecord produces exactly that agreement, so a
// sidecar that disagrees describes a state this store cannot have produced --
// a stale sidecar left beside an amended record, or an edited one. An *absent*
// history is not itself a finding; only a present-and-contradictory one is.
func ValidateStagedHistory(frontmatter map[string]any, history []map[string]any) []string {
	if len(history) == 0 {
		return []string{"disposition history must be a non-empty list, got 0 entries"}
	}

	var findings []string
	for index, entry := range history {
		position := fmt.Sprintf("history entry %d", index+1)
		var missing []string
		for _, key := range StagedHistoryKeys {
			if _, present := entry[key]; !present {
				missing = append(missing, key)
			}
		}
		if len(missing) > 0 {
			findings = append(findings, fmt.Sprintf("%s is missing %s", position, strings.Join(missing, ", ")))
		}
		var unknown []string
		for key := range entry {
			if !containsString(StagedHistoryKeys, key) {
				unknown = append(unknown, key)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			findings = append(findings, fmt.Sprintf(
				"%s carries unknown key(s) %s", position, strings.Join(unknown, ", ")))
		}
		if sequence, ok := historyInt(entry["sequence"]); !ok || sequence != index+1 {
			findings = append(findings, fmt.Sprintf(
				"%s has sequence %#v; history must be ordered and numbered from 1 with no gaps",
				position, entry["sequence"]))
		}
		action, _ := entry["action"].(string)
		if !containsString(StagedDispositionActions, action) {
			findings = append(findings, fmt.Sprintf(
				"%s action must be one of %s; got %#v",
				position, strings.Join(StagedDispositionActions, ", "), entry["action"]))
		}
		if _, ok := entry["diverged_from_proposal"].(bool); !ok {
			findings = append(findings, fmt.Sprintf("%s diverged_from_proposal must be true or false", position))
		}
		for _, key := range []string{"reason", "classification_used", "decided_by", "decided_at"} {
			value, ok := entry[key].(string)
			if !ok || strings.TrimSpace(value) == "" {
				findings = append(findings, fmt.Sprintf("%s %s must be a non-empty string", position, key))
			}
		}
	}
	if len(findings) > 0 {
		return findings
	}

	last := history[len(history)-1]
	disposition, ok := frontmatter["disposition"].(map[string]any)
	if !ok {
		return []string{fmt.Sprintf(
			"the record carries no disposition, but its history records %d decision(s) -- a record nobody "+
				"has decided cannot have a disposition history", len(history))}
	}
	var disagreements []string
	for _, key := range []string{"action", "reason", "classification_used", "diverged_from_proposal", "decided_by"} {
		if last[key] != disposition[key] {
			disagreements = append(disagreements, key)
		}
	}
	if len(disagreements) > 0 {
		findings = append(findings, fmt.Sprintf(
			"the last history entry disagrees with the record's own disposition on %s; the newest history "+
				"entry is that disposition", strings.Join(disagreements, ", ")))
	}
	if frontmatter["status"] != last["action"] {
		findings = append(findings, fmt.Sprintf(
			"the record's status %#v disagrees with its newest history entry's action %#v",
			frontmatter["status"], last["action"]))
	}
	return findings
}

// historyInt reads a sequence number that may have arrived as a JSON number
// (float64) or as a Go int.
func historyInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case float64:
		if typed != float64(int(typed)) {
			return 0, false
		}
		return int(typed), true
	default:
		return 0, false
	}
}

// DecodeStagedHistory reads an `<id>.history.json` sidecar's bytes into the
// loose mapping form ValidateStagedHistory checks. Loose on purpose: decoding
// straight into DispositionEntry would silently discard an unknown key, which
// is precisely what ValidateStagedHistory exists to refuse.
func DecodeStagedHistory(data []byte) ([]map[string]any, error) {
	var history []map[string]any
	if err := json.Unmarshal(data, &history); err != nil {
		return nil, fmt.Errorf("not valid JSON: %w", err)
	}
	return history, nil
}

// StagedHistoryEntries converts validated loose history into typed entries.
// Only call this after ValidateStagedHistory has returned no findings.
func StagedHistoryEntries(history []map[string]any) []DispositionEntry {
	entries := make([]DispositionEntry, 0, len(history))
	for _, entry := range history {
		sequence, _ := historyInt(entry["sequence"])
		action, _ := entry["action"].(string)
		reason, _ := entry["reason"].(string)
		classification, _ := entry["classification_used"].(string)
		diverged, _ := entry["diverged_from_proposal"].(bool)
		decidedBy, _ := entry["decided_by"].(string)
		decidedAt, _ := entry["decided_at"].(string)
		entries = append(entries, DispositionEntry{
			Sequence:             sequence,
			Action:               action,
			Reason:               reason,
			ClassificationUsed:   classification,
			DivergedFromProposal: diverged,
			DecidedBy:            decidedBy,
			DecidedAt:            decidedAt,
		})
	}
	return entries
}

// PutStagedHistory restores a record's disposition history, returning how many
// rows were written.
//
// Append-only, never destructive: this inserts rows and never deletes or
// rewrites one. Re-importing the same export is therefore a no-op (identical
// history already present -> 0 rows written), and an import whose sidecar
// *contradicts* history already in the store is refused rather than
// reconciled. Silently replacing rows here would make this function the one
// way to erase a disposition without leaving evidence, which is precisely what
// DeleteStagedRecord's evidence table exists to prevent.
func (s *Store) PutStagedHistory(recordID string, history []DispositionEntry) (int, error) {
	frontmatter, _, err := s.GetStagedRecord(recordID)
	if err != nil {
		return 0, err
	}
	loose := make([]map[string]any, 0, len(history))
	for _, entry := range history {
		loose = append(loose, map[string]any{
			"sequence":               entry.Sequence,
			"action":                 entry.Action,
			"reason":                 entry.Reason,
			"classification_used":    entry.ClassificationUsed,
			"diverged_from_proposal": entry.DivergedFromProposal,
			"decided_by":             entry.DecidedBy,
			"decided_at":             entry.DecidedAt,
		})
	}
	if findings := ValidateStagedHistory(frontmatter, loose); len(findings) > 0 {
		return 0, &StagedRecordError{
			Message: fmt.Sprintf("disposition history for %q does not satisfy the contract: %s",
				recordID, strings.Join(findings, "; ")),
			Findings: findings,
		}
	}

	existing, err := s.StagedHistory(recordID)
	if err != nil {
		return 0, err
	}
	if len(existing) > 0 {
		if stagedHistoryEqual(existing, history) {
			return 0, nil
		}
		return 0, stagedErrorf(
			"record %q already has a disposition history in this store that differs from the one being "+
				"imported. Refused rather than replaced: this store's history is append-only, and "+
				"overwriting it would erase a decision with no evidence left behind. Reconcile the two "+
				"deliberately -- export this store first to see what it holds.", recordID)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("cannot begin disposition-history restore: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, entry := range history {
		diverged := 0
		if entry.DivergedFromProposal {
			diverged = 1
		}
		if _, err := tx.Exec(
			// observed_actor is left empty deliberately. This restores a
			// decision made elsewhere, at some earlier time, by a process
			// this one cannot speak for -- writing what *this* process sees
			// would claim to have observed something it did not.
			"INSERT INTO staged_record_dispositions (record_id, sequence, action, reason, "+
				"classification_used, diverged_from_proposal, decided_by, decided_at) "+
				"VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
			recordID, entry.Sequence, entry.Action, entry.Reason, entry.ClassificationUsed,
			diverged, entry.DecidedBy, entry.DecidedAt); err != nil {
			return 0, fmt.Errorf("cannot restore disposition history for %q: %w", recordID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("cannot commit disposition-history restore for %q: %w", recordID, err)
	}
	return len(history), nil
}

func stagedHistoryEqual(left, right []DispositionEntry) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
