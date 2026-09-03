package knowledge

import (
	"database/sql"
	"fmt"
)

// Deletion evidence for content that reached the corpus.
//
// The staged half of this has existed since P2 of the previous goal:
// `staged_record_deletions` records the removal of a proposal that never
// entered the corpus. That answered a narrower question than anyone asking
// for their content back is asking, because a staged record is not yet in the
// store.
//
// The structural decision is the same one, for the same reason, and it is the
// only interesting thing here: **no foreign key to the subject.** Evidence
// that a document was deleted has to survive the document, and a foreign key
// would delete the record of the deletion along with the thing deleted —
// leaving a store that is correct and an audit trail that cannot show what
// happened.

const ingestedDeletionSchema = `
CREATE TABLE IF NOT EXISTS ingested_deletions (
  document_id TEXT NOT NULL,
  chunks_removed INTEGER NOT NULL,
  reason TEXT NOT NULL,
  deleted_by TEXT NOT NULL,
  -- What the process observed, beside what the caller asserted. deleted_by is
  -- a string somebody typed; this is not, and no flag reaches it. Stored
  -- separately so a reader can tell a verified subject from a local machine
  -- observation rather than having to trust that they are the same kind of
  -- thing -- see actor_verification in show-staged.
  observed_actor TEXT NOT NULL DEFAULT '',
  deleted_at TEXT NOT NULL
);
`

// IngestedDeletion is one recorded removal of ingested content.
type IngestedDeletion struct {
	DocumentID    string `json:"document_id"`
	ChunksRemoved int    `json:"chunks_removed"`
	Reason        string `json:"reason"`
	DeletedBy     string `json:"deleted_by"`
	ObservedActor string `json:"observed_actor"`
	DeletedAt     string `json:"deleted_at"`
}

// RecordIngestedDeletion writes evidence that ingested content was removed.
//
// Takes the chunk count removed rather than a boolean, because "we deleted
// something" and "we deleted eleven chunks" are different claims and only the
// second can be checked afterwards against what the store now holds.
func (s *Store) RecordIngestedDeletion(deletion IngestedDeletion) error {
	if deletion.DocumentID == "" {
		return stagedErrorf("a deletion record needs a document id")
	}
	if deletion.Reason == "" {
		return stagedErrorf(
			"a deletion requires a reason: an unexplained removal is not an audit trail")
	}
	if deletion.DeletedBy == "" {
		return stagedErrorf("a deletion record needs a deleted_by")
	}
	// Retried, because by the time this runs the content is already gone.
	// Every other write in this package that could lose a lock race retries;
	// this one did not, and it is the one whose failure cannot be undone by
	// running the command again.
	err := execArgsWithBusyRetryFor(s.db, EvidenceBusyTimeout,
		"INSERT INTO ingested_deletions "+
			"(document_id, chunks_removed, reason, deleted_by, observed_actor, deleted_at) "+
			"VALUES (?, ?, ?, ?, ?, ?)",
		deletion.DocumentID, deletion.ChunksRemoved, deletion.Reason,
		deletion.DeletedBy, s.observeActor(), stagedNow())
	if err != nil {
		return fmt.Errorf("cannot record the deletion of %q: %w", deletion.DocumentID, err)
	}
	return nil
}

// IngestedDeletions reads back the evidence, newest first.
//
// Optionally filtered to one document. A deletion record for a document that
// no longer exists is the normal case rather than an anomaly — that is what
// the missing foreign key is for.
func (s *Store) IngestedDeletions(documentID string) ([]IngestedDeletion, error) {
	query := "SELECT document_id, chunks_removed, reason, deleted_by, observed_actor, deleted_at " +
		"FROM ingested_deletions"
	var rows *sql.Rows
	var err error
	if documentID == "" {
		rows, err = s.db.Query(query + " ORDER BY deleted_at DESC")
	} else {
		rows, err = s.db.Query(query+" WHERE document_id = ? ORDER BY deleted_at DESC", documentID)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read deletion evidence: %w", err)
	}
	defer func() { _ = rows.Close() }()

	deletions := []IngestedDeletion{}
	for rows.Next() {
		var d IngestedDeletion
		if err := rows.Scan(&d.DocumentID, &d.ChunksRemoved, &d.Reason,
			&d.DeletedBy, &d.ObservedActor, &d.DeletedAt); err != nil {
			return nil, fmt.Errorf("cannot read a deletion row: %w", err)
		}
		deletions = append(deletions, d)
	}
	return deletions, rows.Err()
}
