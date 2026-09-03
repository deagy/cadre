package retrieval

import (
	"context"
	"fmt"
)

// DeleteIngested removes an ingested document from the corpus.
//
// The capability existed in the Python CLI as `delete-ingested`, was removed
// with it at `b418031e`, and was never rebuilt --
// `roster/knowledge-store/SECURITY.md` says so in its own voice. What
// survived was the ability to delete a *staged* proposal, which is a
// different thing: staged records have not entered the corpus, so deleting
// one answers nobody's request to have their content removed.
//
// Under the previous goal's bar that gap was defensible, and the charter said
// why: "no third party's content enters the store". A colleague's notes are a
// third party's content relative to whoever ingests them, and the person who
// wrote something is the person who can ask for it back. That is what changed.
//
// recall's store has had `DeleteDocument` throughout. The gap was never
// capability, it was reachability: available to somebody willing to write Go
// against the library, and to nobody using the shipped tools.
func (g *Governed) DeleteIngested(documentID string) error {
	if documentID == "" {
		return fmt.Errorf("retrieval: a deletion needs a document id")
	}
	if err := g.store.DeleteDocument(context.Background(), documentID); err != nil {
		return fmt.Errorf("retrieval: cannot delete %q: %w", documentID, err)
	}
	return nil
}

// IngestedChunkCount reports how many chunks the corpus holds for a document.
//
// Used to confirm a deletion did what it said, and deliberately a count rather
// than a boolean: a delete that returns nil and leaves the content in place is
// the shape this project keeps finding, and "how many are left" distinguishes
// gone from partially gone, which a yes/no would hide.
//
// Not derived from a chunk-id convention. An earlier draft of this guessed
// "<id>-chunk-0", which is not a contract recall publishes and would have made
// a wrong answer look like an absent document.
func (g *Governed) IngestedChunkCount(documentID string) (int, error) {
	if documentID == "" {
		return 0, fmt.Errorf("retrieval: a chunk count needs a document id")
	}
	count, err := g.store.DocumentChunkCount(context.Background(), documentID)
	if err != nil {
		return 0, fmt.Errorf("retrieval: %w", err)
	}
	return count, nil
}
