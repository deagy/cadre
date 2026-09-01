package retrieval

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	_ "modernc.org/sqlite"
)

// ErrLegacyStore: the configured file is a knowledge store written by the
// retrieval engine cadre used to have.
var ErrLegacyStore = errors.New("retrieval: this is a pre-migration cadre store, not a recall store")

// legacyTables are the engine's, and only the engine's. recall's schema has
// chunks and embeddings; these five never existed in it.
var legacyTables = []string{"messages", "ingestion_runs", "retrieval_runs", "deletion_runs"}

// RefuseLegacyStore refuses a database written by the deleted engine.
//
// This exists because recall's store initializer is additive: pointed at a
// file that already has a `chunks` table with the old engine's columns, it
// creates what is missing, finds `chunks` present, and returns successfully --
// leaving a file that is neither a valid legacy store nor a valid recall one.
// Every later query then fails with `no such column: c.document_ref`, and the
// operator's first command reported ordinary success.
//
// Silent corruption of the corpus on the first command the quickstart tells
// someone to run is the worst failure this migration can have, so it is
// checked before recall's schema initializer is allowed near the file. The
// staged records in such a file are migrated separately and are not at risk;
// what cannot be salvaged is the corpus, which has to be re-ingested.
func RefuseLegacyStore(database string) error {
	if _, err := os.Stat(database); err != nil {
		return nil // Nothing there yet: recall will create it.
	}

	db, err := sql.Open("sqlite", "file:"+database+"?mode=ro")
	if err != nil {
		// Unreadable for some other reason; let the real open report it.
		return nil
	}
	defer func() { _ = db.Close() }()

	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	present := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil
		}
		present[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil
	}

	var found []string
	for _, table := range legacyTables {
		if present[table] {
			found = append(found, table)
		}
	}
	if len(found) == 0 {
		return nil
	}

	return fmt.Errorf(
		"%w: %s holds the engine's own tables (%s), and recall's schema initializer would "+
			"add its tables alongside them, leaving a file that is neither. Every later search "+
			"would fail with `no such column: c.document_ref`.\n"+
			"  Move it aside and re-ingest its content with `recall upload`. Staged records in "+
			"it are migrated automatically on the next `cadre knowledge` command and are not at "+
			"risk",
		ErrLegacyStore, database, strings.Join(found, ", "))
}
