package knowledge

import (
	"testing"
)

// Deletion evidence must outlive what it describes.
//
// That is the whole reason ingested_deletions carries no foreign key to the
// document, and it is a property worth a test rather than a comment: a
// schema change adding the "missing" constraint would look like tidying up
// and would silently destroy the audit trail at the moment it matters.

func TestDeletionEvidenceSurvivesItsSubject(t *testing.T) {
	store := testStagedStore(t)
	store.observeActor = func() string { return "os:tester git:tester@example.com" }

	if err := store.RecordIngestedDeletion(IngestedDeletion{
		DocumentID:    "doc-that-is-gone",
		ChunksRemoved: 11,
		Reason:        "the author asked for it back",
		DeletedBy:     "steward@example.com",
	}); err != nil {
		t.Fatalf("recording a deletion: %v", err)
	}

	// The document never existed in this store, which is the point: the
	// evidence does not depend on it.
	evidence, err := store.IngestedDeletions("doc-that-is-gone")
	if err != nil {
		t.Fatalf("reading deletion evidence: %v", err)
	}
	if len(evidence) != 1 {
		t.Fatalf("expected one deletion record, got %d", len(evidence))
	}
	got := evidence[0]
	if got.ChunksRemoved != 11 {
		t.Errorf("chunks_removed was %d, want 11", got.ChunksRemoved)
	}
	if got.Reason == "" || got.DeletedBy == "" {
		t.Errorf("evidence is missing reason or deleted_by: %+v", got)
	}
	if got.ObservedActor == "" {
		t.Error("evidence carries no observed actor, so a reader cannot tell an asserted " +
			"name from a verified one")
	}
	if got.DeletedAt == "" {
		t.Error("evidence carries no timestamp")
	}
}

// An unexplained removal is not an audit trail.
func TestADeletionWithoutAReasonIsRefused(t *testing.T) {
	store := testStagedStore(t)
	if err := store.RecordIngestedDeletion(IngestedDeletion{
		DocumentID: "doc-1", DeletedBy: "steward@example.com",
	}); err == nil {
		t.Fatal("a deletion with no reason was recorded")
	}
}

// A deletion record needs a subject and an actor.
func TestADeletionNeedsADocumentAndAnActor(t *testing.T) {
	store := testStagedStore(t)
	if err := store.RecordIngestedDeletion(IngestedDeletion{
		Reason: "because", DeletedBy: "steward@example.com",
	}); err == nil {
		t.Fatal("a deletion with no document id was recorded")
	}
	if err := store.RecordIngestedDeletion(IngestedDeletion{
		DocumentID: "doc-1", Reason: "because",
	}); err == nil {
		t.Fatal("a deletion with no deleted_by was recorded")
	}
}
