package knowledge

// Tests for `ingest-accepted`, the step that makes a steward-accepted staged
// record retrievable -- and therefore the last place a self-approval can still
// be stopped.
//
// Ported from roster/knowledge-store/test/test_accepted_ingest.py on main.

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestTheCorpusReceivesTheStewardsDecision asserts what actually crosses the
// boundary: the record arrives carrying the classification a steward applied,
// the staging source, and the role that marks it as a knowledge record --
// because once it is in the corpus, those three are what a governed retrieval
// filters and cites on.
func TestTheCorpusReceivesTheStewardsDecision(t *testing.T) {
	store := testStagedStore(t)
	recordID := testStageRecord(t, store, "KS-20260101-handoff")
	acceptStagedRecord(t, store, recordID)

	corpus := &recordingCorpus{}
	if _, err := store.IngestAcceptedStagedRecords(
		IngestAcceptedOptions{Corpus: corpus}); err != nil {
		t.Fatalf("ingest failed: %v", err)
	}
	if len(corpus.ingested) != 1 {
		t.Fatalf("corpus received %d records, want 1", len(corpus.ingested))
	}

	received := corpus.ingested[0]
	if received.Classification != "internal" {
		t.Errorf("classification = %q, want the steward's %q", received.Classification, "internal")
	}
	if received.Source != StagedIngestSource {
		t.Errorf("source = %q, want %q", received.Source, StagedIngestSource)
	}
	if received.Role != StagedIngestRole {
		t.Errorf("role = %q, want %q", received.Role, StagedIngestRole)
	}
	if !strings.Contains(received.DocumentID, recordID) {
		t.Errorf("document id %q does not identify the record", received.DocumentID)
	}
	if received.Metadata["source_uri"] != "" {
		t.Errorf("a source URI reached the corpus: %q", received.Metadata["source_uri"])
	}
}

// TestARefusedRecordNeverReachesTheCorpus. The screening refusals are the
// point of this step; a refusal that still handed the content over would be
// theatre.
func TestARefusedRecordNeverReachesTheCorpus(t *testing.T) {
	store := testStagedStore(t)
	recordID := testStageRecord(t, store, "KS-20260101-risky")
	acceptStagedRecord(t, store, recordID)
	frontmatter, _, err := store.GetStagedRecord(recordID)
	if err != nil {
		t.Fatalf("cannot reload record: %v", err)
	}
	frontmatter["untrusted_instruction_risk"] = true
	forceStagedFrontmatter(t, store, recordID, frontmatter)

	corpus := &recordingCorpus{}
	report, err := store.IngestAcceptedStagedRecords(IngestAcceptedOptions{Corpus: corpus})
	if err != nil {
		t.Fatalf("ingest failed: %v", err)
	}
	if len(report.Refused) != 1 {
		t.Fatalf("expected one refusal, got %+v", report)
	}
	if len(corpus.ingested) != 0 {
		t.Fatalf("a refused record was handed to the corpus: %+v", corpus.ingested)
	}
	if ingested, err := store.StagedRecordAlreadyIngested(recordID); err != nil || ingested {
		t.Errorf("a refused record was recorded as ingested (err=%v)", err)
	}
}

// forceStagedFrontmatter writes a record's frontmatter straight into the row,
// bypassing PutStagedRecord's validator.
//
// The illegal states these tests exercise (an accepted record flagged
// untrusted_instruction_risk, an accepted record whose stager is its own
// decider) cannot be produced through any legitimate path -- that is the
// point. Constructing them by hand is the only way to prove the ingest-time
// refusals actually fire rather than being dead code protected by an
// upstream check.
func forceStagedFrontmatter(t *testing.T, store *Store, recordID string, frontmatter map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(frontmatter)
	if err != nil {
		t.Fatalf("cannot encode frontmatter: %v", err)
	}
	if _, err := store.db.Exec(
		"UPDATE staged_records SET status = ?, frontmatter_json = ? WHERE id = ?",
		frontmatter["status"], string(encoded), recordID); err != nil {
		t.Fatalf("cannot force frontmatter: %v", err)
	}
}

// recordingCorpus stands in for the retrievable corpus.
//
// The corpus is recall's now, so these tests assert what this package is
// actually responsible for: which records are sent, at what classification,
// and which are refused before they get there. What recall does with a
// document it is handed is recall's own tested behaviour.
type recordingCorpus struct {
	ingested []CorpusRecord
	fail     error
}

func (c *recordingCorpus) Ingest(record CorpusRecord) (int, error) {
	if c.fail != nil {
		return 0, c.fail
	}
	c.ingested = append(c.ingested, record)
	return 1, nil
}

func (c *recordingCorpus) Destination() string { return "test-corpus" }

func acceptStagedRecord(t *testing.T, store *Store, recordID string) {
	t.Helper()
	if _, err := store.DispositionStagedRecord(recordID, DispositionInput{
		Action:             "accepted",
		Reason:             "reviewed and reproducible",
		ClassificationUsed: "internal",
		DecidedBy:          testStagedSteward,
	}); err != nil {
		t.Fatalf("cannot accept %s: %v", recordID, err)
	}
}

func TestAnAcceptedRecordBecomesRetrievable(t *testing.T) {
	store := testStagedStore(t)
	recordID := testStageRecord(t, store, "KS-20260101-accepted-ingest")
	acceptStagedRecord(t, store, recordID)

	report, err := store.IngestAcceptedStagedRecords(IngestAcceptedOptions{Corpus: &recordingCorpus{}})
	if err != nil {
		t.Fatalf("ingest failed: %v", err)
	}
	if len(report.Ingested) != 1 || report.Ingested[0].ID != recordID {
		t.Fatalf("unexpected ingest report: %+v", report)
	}
	if report.Ingested[0].Chunks == 0 {
		t.Fatal("an ingested record produced no chunks")
	}
	ingested, err := store.StagedRecordAlreadyIngested(recordID)
	if err != nil || !ingested {
		t.Fatalf("record is not in the corpus after ingest (err=%v)", err)
	}
}

func TestAProposedRecordIsNotIngested(t *testing.T) {
	store := testStagedStore(t)
	recordID := testStageRecord(t, store, "KS-20260101-still-proposed")

	report, err := store.IngestAcceptedStagedRecords(IngestAcceptedOptions{Corpus: &recordingCorpus{}})
	if err != nil {
		t.Fatalf("ingest failed: %v", err)
	}
	if len(report.Ingested) != 0 {
		t.Fatalf("a proposed record was ingested: %+v", report.Ingested)
	}
	ingested, err := store.StagedRecordAlreadyIngested(recordID)
	if err != nil || ingested {
		t.Fatalf("a proposed record reached the corpus (err=%v)", err)
	}
}

func TestIngestRefusesAnUntrustedInstructionRiskRecord(t *testing.T) {
	store := testStagedStore(t)
	recordID := testStageRecord(t, store, "KS-20260101-risky-accepted")

	frontmatter := testStagedFrontmatter(recordID)
	frontmatter["status"] = "accepted"
	frontmatter["untrusted_instruction_risk"] = true
	frontmatter["disposition"] = map[string]any{
		"action": "accepted", "reason": "approved during review",
		"classification_used": "internal", "diverged_from_proposal": false,
		"decided_by": testStagedSteward,
	}
	forceStagedFrontmatter(t, store, recordID, frontmatter)

	report, err := store.IngestAcceptedStagedRecords(IngestAcceptedOptions{Corpus: &recordingCorpus{}})
	if err != nil {
		t.Fatalf("ingest failed: %v", err)
	}
	if len(report.Ingested) != 0 {
		t.Fatalf("an injection-risk record was ingested: %+v", report.Ingested)
	}
	if len(report.Refused) != 1 || !strings.Contains(report.Refused[0].Reason, "untrusted_instruction_risk") {
		t.Fatalf("expected an untrusted_instruction_risk refusal, got %+v", report.Refused)
	}
	ingested, err := store.StagedRecordAlreadyIngested(recordID)
	if err != nil || ingested {
		t.Fatalf("a refused record reached the corpus (err=%v)", err)
	}
}

// SEPARATION CHECK 4: a self-approved record is refused at the last step it
// could still be caught.
func TestIngestRefusesASelfApprovedRecord(t *testing.T) {
	store := testStagedStore(t)
	recordID := testStageRecord(t, store, "KS-20260101-self-approved")

	frontmatter := testStagedFrontmatter(recordID)
	frontmatter["status"] = "accepted"
	frontmatter["disposition"] = map[string]any{
		"action": "accepted", "reason": "approved during review",
		"classification_used": "internal", "diverged_from_proposal": false,
		// The same actor as staged_by.
		"decided_by": testStagedProposer,
	}
	forceStagedFrontmatter(t, store, recordID, frontmatter)

	report, err := store.IngestAcceptedStagedRecords(IngestAcceptedOptions{Corpus: &recordingCorpus{}})
	if err != nil {
		t.Fatalf("ingest failed: %v", err)
	}
	if len(report.Ingested) != 0 {
		t.Fatalf("a self-approved record was ingested: %+v", report.Ingested)
	}
	if len(report.Refused) != 1 {
		t.Fatalf("expected exactly one refusal, got %+v", report.Refused)
	}
	if !strings.Contains(report.Refused[0].Reason, testStagedProposer) {
		t.Fatalf("refusal does not name the actor: %s", report.Refused[0].Reason)
	}
	if !strings.Contains(report.Refused[0].Reason, "both staged and dispositioned") {
		t.Fatalf("refusal does not name the rule: %s", report.Refused[0].Reason)
	}
	ingested, err := store.StagedRecordAlreadyIngested(recordID)
	if err != nil || ingested {
		t.Fatalf("a self-approved record reached the corpus (err=%v)", err)
	}
}

// The steward's classification is the decision; the proposer's is a request.
// A proposer must not be able to widen classification by asking.
func TestTheStewardsClassificationWinsOverTheProposers(t *testing.T) {
	store := testStagedStore(t)
	recordID := testStageRecord(t, store, "KS-20260101-classification")
	if _, err := store.DispositionStagedRecord(recordID, DispositionInput{
		Action:               "accepted",
		Reason:               "reviewed, narrowed",
		ClassificationUsed:   "confidential",
		DivergedFromProposal: true,
		DecidedBy:            testStagedSteward,
	}); err != nil {
		t.Fatalf("cannot disposition record: %v", err)
	}

	report, err := store.IngestAcceptedStagedRecords(IngestAcceptedOptions{Corpus: &recordingCorpus{}})
	if err != nil {
		t.Fatalf("ingest failed: %v", err)
	}
	if len(report.Ingested) != 1 || report.Ingested[0].Classification != "confidential" {
		t.Fatalf("the proposer's classification won: %+v", report.Ingested)
	}
}

func TestASecondRunSkipsRatherThanDuplicating(t *testing.T) {
	store := testStagedStore(t)
	recordID := testStageRecord(t, store, "KS-20260101-idempotent")
	acceptStagedRecord(t, store, recordID)

	if _, err := store.IngestAcceptedStagedRecords(IngestAcceptedOptions{Corpus: &recordingCorpus{}}); err != nil {
		t.Fatalf("first ingest failed: %v", err)
	}
	report, err := store.IngestAcceptedStagedRecords(IngestAcceptedOptions{Corpus: &recordingCorpus{}})
	if err != nil {
		t.Fatalf("second ingest failed: %v", err)
	}
	if len(report.Ingested) != 0 {
		t.Fatalf("a second run re-ingested: %+v", report.Ingested)
	}
	if len(report.Skipped) != 1 || report.Skipped[0].Reason != "already in the corpus" {
		t.Fatalf("unexpected skip report: %+v", report.Skipped)
	}
}

func TestDryRunReportsWithoutWriting(t *testing.T) {
	store := testStagedStore(t)
	recordID := testStageRecord(t, store, "KS-20260101-dry-run")
	acceptStagedRecord(t, store, recordID)

	report, err := store.IngestAcceptedStagedRecords(IngestAcceptedOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry-run ingest failed: %v", err)
	}
	if len(report.Ingested) != 1 || !report.Ingested[0].DryRun {
		t.Fatalf("unexpected dry-run report: %+v", report)
	}
	ingested, err := store.StagedRecordAlreadyIngested(recordID)
	if err != nil || ingested {
		t.Fatalf("a dry run wrote to the corpus (err=%v)", err)
	}
}

func TestSelectingByIDIgnoresRecordsNotNamedAndReportsUnknownIDs(t *testing.T) {
	store := testStagedStore(t)
	first := testStageRecord(t, store, "KS-20260101-first")
	second := testStageRecord(t, store, "KS-20260101-second")
	acceptStagedRecord(t, store, first)
	acceptStagedRecord(t, store, second)

	report, err := store.IngestAcceptedStagedRecords(IngestAcceptedOptions{
		Corpus:    &recordingCorpus{},
		RecordIDs: []string{first, "KS-20260101-unknown"},
	})
	if err != nil {
		t.Fatalf("ingest failed: %v", err)
	}
	if len(report.Ingested) != 1 || report.Ingested[0].ID != first {
		t.Fatalf("unexpected selection: %+v", report.Ingested)
	}
	if len(report.NotAccepted) != 1 || report.NotAccepted[0] != "KS-20260101-unknown" {
		t.Fatalf("an unknown id was silently dropped: %+v", report.NotAccepted)
	}
	ingested, err := store.StagedRecordAlreadyIngested(second)
	if err != nil || ingested {
		t.Fatalf("an unnamed record was ingested (err=%v)", err)
	}
}

// Ingested state never lands on the staged record itself -- a flag there
// could disagree with the ingestion evidence, and then two places would claim
// to know.
//
// It used to be derived from the corpus, which was possible only while cadre
// owned one. recall can be asked for a chunk by id but not for what matches a
// metadata scope, so the fact now lives in cadre's own
// staged_record_ingestions table. That is also where it belongs: a steward's
// acceptance having been carried out is governance evidence, and it should
// outlive any particular store being rebuilt.
func TestIngestedStateIsNotWrittenOntoTheRecord(t *testing.T) {
	store := testStagedStore(t)
	recordID := testStageRecord(t, store, "KS-20260101-derived")
	acceptStagedRecord(t, store, recordID)
	if _, err := store.IngestAcceptedStagedRecords(IngestAcceptedOptions{Corpus: &recordingCorpus{}}); err != nil {
		t.Fatalf("ingest failed: %v", err)
	}

	frontmatter, _, err := store.GetStagedRecord(recordID)
	if err != nil {
		t.Fatalf("cannot reload record: %v", err)
	}
	for key := range frontmatter {
		if strings.Contains(key, "ingest") {
			t.Fatalf("the staged record grew an ingest-state field %q", key)
		}
	}
}
