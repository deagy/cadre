package knowledge

// Negative tests for the authorship/approval separation invariant.
//
// Ported from the deleted Python suite (roster/knowledge-store/test/
// test_staged_cli.py, test_staged_store.py, test_accepted_ingest.py on main).
// Every test here asserts a *refusal* and, just as importantly, that the
// refusal left the store untouched -- a guard that refuses loudly but writes
// anyway is not a guard.
//
// Each test names the check it covers so a future edit that removes a guard
// fails a test whose name says what was removed.

import (
	"errors"
	"strings"
	"testing"
)

// SEPARATION CHECK 2: the proposer cannot disposition their own record.
func TestDispositionRefusesTheProposerAsDecider(t *testing.T) {
	store := testStagedStore(t)
	recordID := testStageRecord(t, store, "KS-20260101-self-disposition")

	_, err := store.DispositionStagedRecord(recordID, DispositionInput{
		Action:             "accepted",
		Reason:             "self approval",
		ClassificationUsed: "internal",
		DecidedBy:          testStagedProposer,
	})
	if err == nil {
		t.Fatal("expected the proposer's own disposition to be refused")
	}
	if !strings.Contains(err.Error(), "cannot also disposition it") {
		t.Fatalf("refusal does not name the separation rule: %v", err)
	}

	// Refused means untouched: status unchanged and no history row.
	frontmatter, _, err := store.GetStagedRecord(recordID)
	if err != nil {
		t.Fatalf("cannot reload record: %v", err)
	}
	if got := StagedString(frontmatter, "status"); got != "proposed" {
		t.Fatalf("status changed despite refusal: %q", got)
	}
	history, err := store.StagedHistory(recordID)
	if err != nil {
		t.Fatalf("cannot read history: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("a refused disposition left %d history row(s)", len(history))
	}
}

// A disposition by anyone other than the stager is the legitimate case, and
// must still work -- otherwise the guard above could be "passing" by refusing
// everything.
func TestDispositionBySomeoneOtherThanTheProposerSucceeds(t *testing.T) {
	store := testStagedStore(t)
	recordID := testStageRecord(t, store, "KS-20260101-legitimate")

	result, err := store.DispositionStagedRecord(recordID, DispositionInput{
		Action:             "accepted",
		Reason:             "reviewed and reproducible",
		ClassificationUsed: "internal",
		DecidedBy:          testStagedSteward,
	})
	if err != nil {
		t.Fatalf("a legitimate disposition was refused: %v", err)
	}
	if result.Status != "accepted" || result.Sequence != 1 {
		t.Fatalf("unexpected disposition result: %+v", result)
	}
}

// A disposition with no reason is not an audit trail. Whitespace is not a
// reason either.
func TestDispositionRefusesAnEmptyReason(t *testing.T) {
	store := testStagedStore(t)
	recordID := testStageRecord(t, store, "KS-20260101-no-reason")

	_, err := store.DispositionStagedRecord(recordID, DispositionInput{
		Action:             "accepted",
		Reason:             "   ",
		ClassificationUsed: "internal",
		DecidedBy:          testStagedSteward,
	})
	if err == nil || !strings.Contains(err.Error(), "not an audit trail") {
		t.Fatalf("expected an empty reason to be refused, got %v", err)
	}
	history, err := store.StagedHistory(recordID)
	if err != nil {
		t.Fatalf("cannot read history: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("a refused disposition left %d history row(s)", len(history))
	}
}

// The automatic-defer rule reaches through disposition: an illegal decision
// must leave no history row, because the validator runs before the write.
func TestAnIllegalDispositionLeavesNoHistoryRow(t *testing.T) {
	store := testStagedStore(t)
	frontmatter := testStagedFrontmatter("KS-20260101-risky")
	frontmatter["untrusted_instruction_risk"] = true
	recordID, err := store.PutStagedRecord(frontmatter, testStagedBody)
	if err != nil {
		t.Fatalf("cannot stage risky record: %v", err)
	}

	_, err = store.DispositionStagedRecord(recordID, DispositionInput{
		Action:             "accepted",
		Reason:             "looks fine to me",
		ClassificationUsed: "internal",
		DecidedBy:          testStagedSteward,
	})
	if err == nil {
		t.Fatal("expected accepting an injection-risk record to be refused")
	}
	history, err := store.StagedHistory(recordID)
	if err != nil {
		t.Fatalf("cannot read history: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("an illegal disposition left %d history row(s)", len(history))
	}
	reloaded, _, err := store.GetStagedRecord(recordID)
	if err != nil {
		t.Fatalf("cannot reload record: %v", err)
	}
	if got := StagedString(reloaded, "status"); got != "proposed" {
		t.Fatalf("status changed despite refusal: %q", got)
	}
}

// SEPARATION CHECK 5 (the deletion counterpart): a still-'proposed' record has
// no decision to protect, so its own proposer may withdraw it.
func TestAStillProposedRecordMayBeDeletedByItsOwnProposer(t *testing.T) {
	store := testStagedStore(t)
	recordID := testStageRecord(t, store, "KS-20260101-withdrawn")

	result, err := store.DeleteStagedRecord(recordID, DeleteStagedInput{
		Reason:    "withdrawing my own draft",
		DeletedBy: testStagedProposer,
	})
	if err != nil {
		t.Fatalf("withdrawing an undecided draft was refused: %v", err)
	}
	if result.Status != "deleted" || !result.EvidenceRetained {
		t.Fatalf("unexpected deletion result: %+v", result)
	}
	evidence, err := store.StagedDeletionEvidenceRows()
	if err != nil {
		t.Fatalf("cannot read deletion evidence: %v", err)
	}
	if len(evidence) != 1 || evidence[0].RecordID != recordID {
		t.Fatalf("deletion evidence missing or wrong: %+v", evidence)
	}
}

// SEPARATION CHECK 5: once a record carries any disposition, the proposer may
// not be the one who erases the evidence of it.
func TestADispositionedRecordCannotBeDeletedByItsOwnProposer(t *testing.T) {
	store := testStagedStore(t)
	recordID := testStageRecord(t, store, "KS-20260101-decided")
	if _, err := store.DispositionStagedRecord(recordID, DispositionInput{
		Action:             "rejected",
		Reason:             "not reproducible",
		ClassificationUsed: "internal",
		DecidedBy:          testStagedSteward,
	}); err != nil {
		t.Fatalf("cannot disposition record: %v", err)
	}

	_, err := store.DeleteStagedRecord(recordID, DeleteStagedInput{
		Reason:       "trying to erase the outcome",
		DeletedBy:    testStagedProposer,
		AuthorizedBy: "an authorized human",
	})
	if err == nil {
		t.Fatal("expected the proposer's deletion of a decided record to be refused")
	}
	if !strings.Contains(err.Error(), "already carries a disposition") {
		t.Fatalf("refusal does not name the reason: %v", err)
	}
	// Refused means untouched and unrecorded.
	if _, _, err := store.GetStagedRecord(recordID); err != nil {
		t.Fatalf("record was removed despite refusal: %v", err)
	}
	evidence, err := store.StagedDeletionEvidenceRows()
	if err != nil {
		t.Fatalf("cannot read deletion evidence: %v", err)
	}
	if len(evidence) != 0 {
		t.Fatalf("a refused deletion wrote %d evidence row(s)", len(evidence))
	}
}

func TestADispositionedRecordCanBeDeletedBySomeoneOtherThanItsProposer(t *testing.T) {
	store := testStagedStore(t)
	recordID := testStageRecord(t, store, "KS-20260101-superseded")
	if _, err := store.DispositionStagedRecord(recordID, DispositionInput{
		Action:             "accepted",
		Reason:             "reviewed",
		ClassificationUsed: "internal",
		DecidedBy:          testStagedSteward,
	}); err != nil {
		t.Fatalf("cannot disposition record: %v", err)
	}

	result, err := store.DeleteStagedRecord(recordID, DeleteStagedInput{
		Reason:       "superseded",
		DeletedBy:    testStagedProposer + "-someone-else",
		AuthorizedBy: "an authorized human",
	})
	if err != nil {
		t.Fatalf("a legitimate deletion was refused: %v", err)
	}
	if result.StatusAtDeletion != "accepted" {
		t.Fatalf("unexpected deletion result: %+v", result)
	}
}

// The human-authorization gate: deleting an accepted record reverses a
// steward's decision.
func TestDeletingAnAcceptedRecordRequiresAnAuthorizedHuman(t *testing.T) {
	store := testStagedStore(t)
	recordID := testStageRecord(t, store, "KS-20260101-accepted")
	if _, err := store.DispositionStagedRecord(recordID, DispositionInput{
		Action:             "accepted",
		Reason:             "reviewed",
		ClassificationUsed: "internal",
		DecidedBy:          testStagedSteward,
	}); err != nil {
		t.Fatalf("cannot disposition record: %v", err)
	}

	for name, authorizer := range map[string]string{
		"missing":    "",
		"whitespace": "   ",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := store.DeleteStagedRecord(recordID, DeleteStagedInput{
				Reason:       "no longer wanted",
				DeletedBy:    testStagedSteward,
				AuthorizedBy: authorizer,
			})
			if err == nil {
				t.Fatal("expected deletion of an accepted record to require --authorized-by")
			}
			if !strings.Contains(err.Error(), "requires an authorized human") {
				t.Fatalf("refusal does not name the gate: %v", err)
			}
			if _, _, err := store.GetStagedRecord(recordID); err != nil {
				t.Fatalf("record was removed despite refusal: %v", err)
			}
		})
	}
}

func TestDeleteRefusesAnEmptyReason(t *testing.T) {
	store := testStagedStore(t)
	recordID := testStageRecord(t, store, "KS-20260101-unexplained")

	_, err := store.DeleteStagedRecord(recordID, DeleteStagedInput{Reason: "  ", DeletedBy: testStagedSteward})
	if err == nil || !strings.Contains(err.Error(), "indistinguishable from data loss") {
		t.Fatalf("expected an unexplained deletion to be refused, got %v", err)
	}
}

// StagedRecordIsSelfApproved is the shared predicate behind checks 3 and 4;
// the two paths must not be able to drift into disagreeing about what a
// self-approval is.
func TestStagedRecordIsSelfApprovedRecognisesTheShape(t *testing.T) {
	proposed := testStagedFrontmatter("KS-20260101-shape")
	if StagedRecordIsSelfApproved(proposed) {
		t.Fatal("a record with no disposition is not a self-approval")
	}

	selfApproved := testStagedFrontmatter("KS-20260101-shape")
	selfApproved["status"] = "accepted"
	selfApproved["disposition"] = map[string]any{
		"action": "accepted", "reason": "approved during review",
		"classification_used": "internal", "diverged_from_proposal": false,
		"decided_by": testStagedProposer,
	}
	if !StagedRecordIsSelfApproved(selfApproved) {
		t.Fatal("stager == decider must read as a self-approval")
	}

	// An empty decided_by is a malformed disposition, not a self-approval --
	// the contract validator refuses it on its own terms, and treating it as a
	// self-approval here would produce a misleading refusal message.
	blank := testStagedFrontmatter("KS-20260101-shape")
	blank["staged_by"] = ""
	blank["disposition"] = map[string]any{"decided_by": ""}
	if StagedRecordIsSelfApproved(blank) {
		t.Fatal("a blank decided_by must not read as a self-approval")
	}
}

// Deleting an unknown record names it rather than reporting a silent success.
func TestDeletingAnUnknownRecordNamesIt(t *testing.T) {
	store := testStagedStore(t)
	_, err := store.DeleteStagedRecord("KS-20260101-nope", DeleteStagedInput{
		Reason: "does not exist", DeletedBy: testStagedSteward,
	})
	if !errors.Is(err, ErrStagedRecordNotFound) {
		t.Fatalf("expected ErrStagedRecordNotFound, got %v", err)
	}
	if !strings.Contains(err.Error(), "KS-20260101-nope") {
		t.Fatalf("error does not name the record: %v", err)
	}
}

// An asserted actor sits beside the observation; it never replaces it.
//
// This is the property AC-4 turns on, and the one way this change could be
// made worthless. If `--deleted-by alice` overwrote what the process saw,
// the record would be exactly as unverified as before and would now look
// authoritative -- worse than the string it replaced, because a reader would
// have no way to tell.
func TestAnAssertedActorDoesNotReplaceTheObservedOne(t *testing.T) {
	store := testStagedStore(t)
	recordID := testStageRecord(t, store, "KS-20260101-observed")

	if _, err := store.DeleteStagedRecord(recordID, DeleteStagedInput{
		Reason:    "checking the observation survives an assertion",
		DeletedBy: "a-name-nobody-verified",
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	rows, err := store.StagedDeletionEvidenceRows()
	if err != nil {
		t.Fatalf("reading evidence: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one evidence row, got %d", len(rows))
	}
	row := rows[0]

	if row.DeletedBy != "a-name-nobody-verified" {
		t.Fatalf("the asserted actor was lost: %q", row.DeletedBy)
	}
	if row.ObservedActor == "" {
		t.Fatal("no observation was recorded beside the assertion.\n" +
			"  The row then names an actor nobody verified and says nothing about what\n" +
			"  the process actually saw, which is the state this column exists to end.")
	}
	if row.ObservedActor == row.DeletedBy {
		t.Fatalf("the observation equals the assertion (%q).\n"+
			"  A flag must not be able to set what the process claims to have seen.",
			row.ObservedActor)
	}
	// The observation carries its source, so it cannot be misread as a name.
	if !strings.Contains(row.ObservedActor, ":") {
		t.Fatalf("observed actor %q reads as a bare name; it must name its source",
			row.ObservedActor)
	}
}

// All four actor flags, not just the deletion path.
//
// The first attempt at this criterion covered `--deleted-by` and
// `--authorized-by` and argued deletion was where an unverifiable actor
// costs most. Verification disagreed, and the argument that settled it came
// from the spec itself: its neighbouring criterion rules out disclosure that
// lives "not only in SECURITY.md", so a runtime record is what the bar
// means. `staged_by` and `decided_by` get the same treatment.
func TestStagingAndDispositionRecordWhatWasObserved(t *testing.T) {
	store := testStagedStore(t)
	recordID := testStageRecord(t, store, "KS-20260101-four-flags")

	observed, err := store.StagedObservedActor(recordID)
	if err != nil {
		t.Fatalf("reading the staged observation: %v", err)
	}
	if observed == "" {
		t.Fatal("propose recorded no observation beside the asserted staged_by")
	}
	if observed == testStagedProposer {
		t.Fatalf("the observation equals the asserted stager (%q); a flag must not set it", observed)
	}

	if _, err := store.DispositionStagedRecord(recordID, DispositionInput{
		Action:             "accepted",
		Reason:             "checking the disposition observation",
		ClassificationUsed: "internal",
		DecidedBy:          "a-different-steward",
	}); err != nil {
		t.Fatalf("disposition: %v", err)
	}

	history, err := store.StagedHistory(recordID)
	if err != nil {
		t.Fatalf("reading history: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected one disposition, got %d", len(history))
	}
	entry := history[0]
	if entry.DecidedBy != "a-different-steward" {
		t.Fatalf("the asserted decider was lost: %q", entry.DecidedBy)
	}
	if entry.ObservedActor == "" {
		t.Fatal("disposition-staged recorded no observation beside the asserted decided_by")
	}
	if entry.ObservedActor == entry.DecidedBy {
		t.Fatalf("the observation equals the assertion (%q)", entry.ObservedActor)
	}
	if !strings.Contains(entry.ObservedActor, ":") {
		t.Fatalf("observed actor %q reads as a bare name", entry.ObservedActor)
	}
}
