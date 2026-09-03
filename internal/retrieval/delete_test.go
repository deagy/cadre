package retrieval

import (
	"path/filepath"
	"strings"
	"testing"
)

// Deleting ingested content must actually remove it.
//
// The weak reading of this criterion is "delete returned nil". A delete that
// reports success and leaves the content in place is the shape this project
// keeps finding, and the only thing separating the two is looking afterwards.
//
// So this asserts presence *first*. Without that, "the document is gone" is
// satisfied by a store that never had it, which is a test that passes whatever
// the code does.

func ingestOne(t *testing.T, governed *Governed, documentID string) {
	t.Helper()
	chunks, err := governed.Ingest(Record{
		DocumentID:     documentID,
		Source:         "test",
		Title:          "A document that will be deleted",
		Classification: "internal",
		Role:           "tester",
		ContentHash:    "hash-" + documentID,
		Content: strings.Repeat(
			"Content belonging to "+documentID+", long enough that the chunker keeps it. ", 30),
	})
	if err != nil {
		t.Fatalf("ingesting %s: %v", documentID, err)
	}
	if chunks == 0 {
		t.Fatalf("ingesting %s produced no chunks", documentID)
	}
}

func TestDeletingIngestedContentRemovesIt(t *testing.T) {
	governed := testGoverned(t)
	const documentID = "doc-to-delete"

	ingestOne(t, governed, documentID)

	before, err := governed.IngestedChunkCount(documentID)
	if err != nil {
		t.Fatalf("counting before deletion: %v", err)
	}
	if before == 0 {
		t.Fatal("the document is absent before deletion; this test would pass on a store " +
			"that never held anything")
	}

	if err := governed.DeleteIngested(documentID); err != nil {
		t.Fatalf("deleting %s: %v", documentID, err)
	}

	after, err := governed.IngestedChunkCount(documentID)
	if err != nil {
		t.Fatalf("counting after deletion: %v", err)
	}
	if after != 0 {
		t.Fatalf("%d chunk(s) of %s survived deletion, which returned no error.\n"+
			"A delete that reports success and leaves content behind is worse than one "+
			"that fails: nothing downstream will look again.", after, documentID)
	}
}

// Deleting one document must not delete another.
//
// Over-deletion is the failure mode that would make this unusable, and it is
// invisible to a test that only ever holds one document.
func TestDeletingOneDocumentLeavesTheOthers(t *testing.T) {
	governed := testGoverned(t)

	ingestOne(t, governed, "doc-keep")
	ingestOne(t, governed, "doc-remove")

	if err := governed.DeleteIngested("doc-remove"); err != nil {
		t.Fatalf("deleting doc-remove: %v", err)
	}

	kept, err := governed.IngestedChunkCount("doc-keep")
	if err != nil {
		t.Fatalf("counting doc-keep: %v", err)
	}
	if kept == 0 {
		t.Fatal("deleting one document removed another")
	}
}

// A deletion needs a document id.
func TestDeletingWithoutADocumentIDRefuses(t *testing.T) {
	governed := testGoverned(t)
	if err := governed.DeleteIngested(""); err == nil {
		t.Fatal("an empty document id was accepted; a delete with no subject is not a delete")
	}
}

func testGoverned(t *testing.T) *Governed {
	t.Helper()
	governed, err := OpenForIngest(Options{
		Database:     filepath.Join(t.TempDir(), "corpus.db"),
		Namespace:    "test",
		EmbedderName: "local-hashing",
		Dimensions:   128,
	}, stubProvider{})
	if err != nil {
		t.Fatalf("opening a governed store: %v", err)
	}
	t.Cleanup(func() { _ = governed.Close() })
	return governed
}
