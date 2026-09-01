package knowledge

// Ingest steward-accepted staged records into the retrievable corpus.
//
// The gap this closes, stated plainly: capture worked and the pipeline stopped
// one action short of usefulness. Findings are proposed with `propose`, land
// in staged_records, and a steward dispositions them with
// `disposition-staged`. Nothing then moved an accepted record into the corpus,
// and search scores only ingested chunks -- so an accepted record was
// permanently unreachable by any query.
//
// **Staging still is not ingestion, and accepting still is not ingesting.**
// This does not collapse those steps; it supplies the missing third one. The
// steward decides (DispositionStagedRecord, which structurally forbids the
// proposer from dispositioning their own record); this executes a decision
// already made.
//
// **This step is steward authority and is deliberately NOT gated on a human
// approval flag.** An earlier session added such a gate here, found it caught
// nothing -- the decision it would have re-confirmed was already made and
// already separation-checked at `disposition-staged`, and the flag was a
// caller-asserted string that no path could refuse on -- and removed it. Do
// not reintroduce it: `ingest-accepted` takes no `--authorized-by` and no
// `--decided-by`, because it takes no decision. What it does do is refuse to
// execute a decision that was never validly taken, which is a different act
// and is what the checks below are.
//
// Three rules it enforces, none of which is new policy:
//
//   - `untrusted_instruction_risk` is disqualifying. The contract already
//     forces such a record to 'deferred', so an accepted one should not exist;
//     this refuses it anyway rather than trusting an invariant enforced
//     elsewhere.
//   - The steward's `classification_used` wins over the proposer's
//     `proposed_classification`. The disposition is the authoritative decision;
//     the proposal is a request. Ingesting at the proposed level would let a
//     proposer widen classification by asking.
//   - Ingested state is derived, never recorded twice. A record is ingested iff
//     a message with its id exists in the corpus. A second `ingested` flag on
//     the staged record could disagree with the corpus, and then two places
//     would claim to know.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/deagy/cadre/cli/internal/retrieval"
	"github.com/deagy/cadre/cli/internal/textutil"
)

// StagedIngestSource is a dedicated ingest source, so an accepted finding is
// attributable and filterable as what it is. Retrieval requires an explicit
// source selection, so a caller reaches these deliberately rather than by
// accident.
const StagedIngestSource = "proposed-knowledge"

// StagedIngestRole marks ingested staged records in the corpus.
const StagedIngestRole = "knowledge-record"

// StagedIngestOutcome is one record's result: ingested, skipped, or refused.
type StagedIngestOutcome struct {
	ID             string `json:"id"`
	Reason         string `json:"reason,omitempty"`
	Classification string `json:"classification,omitempty"`
	Chunks         int    `json:"chunks,omitempty"`
	DryRun         bool   `json:"dry_run,omitempty"`
}

// StagedIngestReport is a per-record report rather than a count. A steward
// running this needs to know which findings became retrievable and which were
// refused, and a summary number answers neither.
type StagedIngestReport struct {
	Ingested    []StagedIngestOutcome `json:"ingested"`
	Skipped     []StagedIngestOutcome `json:"skipped"`
	Refused     []StagedIngestOutcome `json:"refused"`
	NotAccepted []string              `json:"not_accepted"`
	DryRun      bool                  `json:"dry_run"`
}

// Corpus is where an accepted record becomes retrievable.
//
// An interface rather than a concrete store because the destination is no
// longer cadre's: retrieval lives in recall, reached through
// internal/retrieval, and this package has no business knowing how. What it
// owns is the decision to send a record there and the evidence that it did.
type Corpus interface {
	// Ingest writes one record and reports how many chunks it became.
	Ingest(record CorpusRecord) (int, error)
	// Destination names the store written to, for the ingestion evidence.
	Destination() string
}

// CorpusRecord is one accepted staged record on its way to being retrievable.
//
// An alias rather than a second struct with the same fields: two declarations
// of one shape is the defect class this consolidation keeps finding, and an
// alias cannot drift from the destination's.
//
// SourceURI is deliberately absent from it rather than optional: it may reveal
// a local path, and the knowledge-use policy's redact-by-default rule applies
// to a staged record's provenance exactly as it does to a citation's.
type CorpusRecord = retrieval.Record

// StagedRecordAlreadyIngested reports whether a record was made retrievable.
//
// Read from cadre's own ingestion evidence rather than from the corpus. It
// used to query the corpus directly, which was possible only while cadre
// owned it; recall can be asked for a chunk by id but cannot be asked what
// matches a metadata scope. Keeping the record here is also the better answer
// for governance: "a steward made this retrievable, at this time, into this
// store" is cadre's fact to keep, and it survives the corpus being rebuilt.
func (s *Store) StagedRecordAlreadyIngested(recordID string) (bool, error) {
	var found int
	err := s.db.QueryRow(
		"SELECT 1 FROM staged_record_ingestions WHERE record_id = ? LIMIT 1",
		recordID).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("cannot check whether %q is already ingested: %w", recordID, err)
	}
	return true, nil
}

// recordStagedIngestion writes the evidence that a record became retrievable.
func (s *Store) recordStagedIngestion(
	recordID, documentID, corpus, classification string, chunks int,
) error {
	_, err := s.db.Exec(`
INSERT OR REPLACE INTO staged_record_ingestions
  (record_id, document_id, corpus, classification, chunk_count, ingested_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		recordID, documentID, corpus, classification, chunks, nowISO())
	if err != nil {
		return fmt.Errorf("cannot record the ingestion of %q: %w", recordID, err)
	}
	return nil
}

// stagedIngestClassification resolves the classification to ingest at. The
// steward's disposition.classification_used is authoritative; the proposer's
// proposed_classification is only a fallback for a record dispositioned before
// that field existed.
func stagedIngestClassification(frontmatter map[string]any) (string, error) {
	if disposition, ok := frontmatter["disposition"].(map[string]any); ok {
		if used, ok := disposition["classification_used"].(string); ok && used != "" {
			return used, nil
		}
	}
	if proposed := StagedString(frontmatter, "proposed_classification"); proposed != "" {
		return proposed, nil
	}
	return "", fmt.Errorf("no classification to ingest at. The steward's " +
		"disposition.classification_used is authoritative and is missing")
}

// IngestAcceptedOptions selects which accepted records to ingest.
type IngestAcceptedOptions struct {
	// RecordIDs, when non-empty, restricts ingestion to these ids. An id that
	// is not an accepted record is reported in NotAccepted rather than
	// silently dropped.
	RecordIDs []string
	// DryRun reports what would be ingested and refused, without writing.
	DryRun bool
	// Corpus is where accepted records become retrievable. Required unless
	// DryRun: making a record retrievable with nowhere to put it is not a
	// thing this can do quietly.
	Corpus Corpus
}

// IngestAcceptedStagedRecords ingests every accepted staged record that is not
// already in the corpus.
func (s *Store) IngestAcceptedStagedRecords(options IngestAcceptedOptions) (*StagedIngestReport, error) {
	wanted := map[string]bool{}
	for _, id := range options.RecordIDs {
		wanted[id] = true
	}

	accepted, err := s.ListStagedRecords("accepted")
	if err != nil {
		return nil, err
	}

	report := &StagedIngestReport{
		Ingested:    []StagedIngestOutcome{},
		Skipped:     []StagedIngestOutcome{},
		Refused:     []StagedIngestOutcome{},
		NotAccepted: []string{},
		DryRun:      options.DryRun,
	}
	if !options.DryRun && options.Corpus == nil {
		return nil, fmt.Errorf(
			"cannot ingest accepted records: no corpus to make them retrievable in")
	}

	for _, summary := range accepted {
		recordID := summary.ID
		if len(wanted) > 0 && !wanted[recordID] {
			continue
		}
		frontmatter, body, err := s.GetStagedRecord(recordID)
		if err != nil {
			if errors.Is(err, ErrStagedRecordNotFound) {
				continue // listed, then vanished
			}
			return nil, err
		}

		if refusal := stagedIngestRefusal(frontmatter); refusal != "" {
			report.Refused = append(report.Refused, StagedIngestOutcome{ID: recordID, Reason: refusal})
			continue
		}

		ingested, err := s.StagedRecordAlreadyIngested(recordID)
		if err != nil {
			return nil, err
		}
		if ingested {
			report.Skipped = append(report.Skipped, StagedIngestOutcome{ID: recordID, Reason: "already in the corpus"})
			continue
		}

		classification, err := stagedIngestClassification(frontmatter)
		if err != nil {
			report.Refused = append(report.Refused, StagedIngestOutcome{
				ID:     recordID,
				Reason: fmt.Sprintf("%s: %s", recordID, err),
			})
			continue
		}

		if options.DryRun {
			report.Ingested = append(report.Ingested, StagedIngestOutcome{
				ID: recordID, Classification: classification, DryRun: true,
			})
			continue
		}

		outcome, err := s.ingestOneStagedRecord(recordID, frontmatter, body, classification, options.Corpus)
		if err != nil {
			return nil, err
		}
		if outcome.Reason != "" {
			report.Refused = append(report.Refused, outcome)
			continue
		}
		report.Ingested = append(report.Ingested, outcome)
	}

	if len(wanted) > 0 {
		acceptedIDs := map[string]bool{}
		for _, summary := range accepted {
			acceptedIDs[summary.ID] = true
		}
		for id := range wanted {
			if !acceptedIDs[id] {
				report.NotAccepted = append(report.NotAccepted, id)
			}
		}
		sort.Strings(report.NotAccepted)
	}
	return report, nil
}

// stagedIngestRefusal returns the reason this accepted record must not be
// ingested, or "" when there is none.
//
// SEPARATION CHECK 4 of 4 (roster/knowledge-store/SECURITY.md): the stager
// cannot also be the decider, checked here too.
// DispositionStagedRecord refuses decided_by == staged_by, and `propose`
// refuses a record that arrives already dispositioned. Neither covers
// `import-staged`, which legitimately takes decided records and is how an
// outside corpus enters -- and that path has its own check, but this one does
// not assume it held. Ingestion is the step that makes a record retrievable,
// so it is the last place a self-approval can still be stopped.
//
// The untrusted-instruction-risk refusal is there for the same reason: the
// contract already forces such a record to 'deferred', so an accepted one
// should not exist, and it is refused here rather than trusted to have been
// caught upstream.
func stagedIngestRefusal(frontmatter map[string]any) string {
	if risk := frontmatter["untrusted_instruction_risk"]; stagedRiskIsElevated(risk) {
		return fmt.Sprintf(
			"untrusted_instruction_risk is %#v. The staged-record contract forces such a record to "+
				"'deferred'; an accepted one should not exist, and it is refused here rather than trusted "+
				"to have been caught upstream.", risk)
	}
	if StagedRecordIsSelfApproved(frontmatter) {
		return fmt.Sprintf(
			"%q both staged and dispositioned this record. Authorship and approval separation is the "+
				"reason a proposing agent may write here at all, so a self-approved record is refused at "+
				"the last step it could still be caught -- ingestion is what makes a record retrievable.",
			StagedDecidedBy(frontmatter))
	}
	return ""
}

// ingestOneStagedRecord writes one accepted record into the corpus. A non-empty
// Reason on the returned outcome means the record was refused during
// screening, not that an error occurred.
func (s *Store) ingestOneStagedRecord(
	recordID string, frontmatter map[string]any, body, classification string, corpus Corpus,
) (StagedIngestOutcome, error) {
	title := StagedString(frontmatter, "title")
	if title == "" {
		title = recordID
	}
	// Title first so a retrieved chunk carries the claim, not just its
	// supporting prose -- a scorer matching the body of a finding whose title
	// states the finding is the shape that makes results look irrelevant.
	content := fmt.Sprintf("%s\n\n%s\n", title, strings.TrimSpace(body))

	// Secret redaction and injection screening apply to a staged record exactly
	// as to any other ingested content. A finding is authored text like any
	// other, and "an agent wrote it" is not provenance.
	protected := textutil.ProtectContent(content, true)
	if protected.InjectionRisk {
		return StagedIngestOutcome{
			ID: recordID,
			Reason: "content screening flagged injection risk; ingesting it would put unvetted " +
				"instruction-shaped text into the retrievable corpus",
		}, nil
	}

	redactions := protected.Redactions
	if redactions == nil {
		redactions = []string{}
	}
	redactionsJSON, err := json.Marshal(redactions)
	if err != nil {
		return StagedIngestOutcome{}, fmt.Errorf("cannot encode redactions for %q: %w", recordID, err)
	}
	metadataJSON, err := json.Marshal(map[string]any{
		"staged_record_id":   recordID,
		"recommended_action": StagedString(frontmatter, "recommended_action"),
		"source_scope":       StagedString(frontmatter, "source_scope"),
		"content_digest":     StagedString(frontmatter, "content_digest"),
		"decided_by":         StagedDecidedBy(frontmatter),
	})
	if err != nil {
		return StagedIngestOutcome{}, fmt.Errorf("cannot encode metadata for %q: %w", recordID, err)
	}

	documentID := StagedIngestSource + ":" + recordID
	chunks, err := corpus.Ingest(CorpusRecord{
		DocumentID:     documentID,
		Source:         StagedIngestSource,
		Title:          title,
		Classification: classification,
		Role:           StagedIngestRole,
		Content:        protected.Content,
		ContentHash:    StagedString(frontmatter, "content_digest"),
		Metadata: map[string]string{
			"staged_record_id":   recordID,
			"recommended_action": StagedString(frontmatter, "recommended_action"),
			"source_scope":       StagedString(frontmatter, "source_scope"),
			"decided_by":         StagedDecidedBy(frontmatter),
			"redactions":         string(redactionsJSON),
			"metadata":           string(metadataJSON),
		},
	})
	if err != nil {
		return StagedIngestOutcome{}, fmt.Errorf("cannot make %q retrievable: %w", recordID, err)
	}

	if err := s.recordStagedIngestion(
		recordID, documentID, corpus.Destination(), classification, chunks); err != nil {
		return StagedIngestOutcome{}, err
	}

	return StagedIngestOutcome{ID: recordID, Classification: classification, Chunks: chunks}, nil
}
