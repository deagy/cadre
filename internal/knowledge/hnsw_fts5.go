package knowledge

import (
	"database/sql"
	"fmt"
	"sync"
	"time"
)

// FTS5Index provides full-text search indexing using SQLite FTS5.
// For testing without CGO, uses in-memory document storage.
type FTS5Index struct {
	mu    sync.RWMutex
	db    *sql.DB
	ready bool
	// available is true only when SQLite really created the fts5 virtual
	// table. The build of mattn/go-sqlite3 that ships without the
	// `sqlite_fts5` tag reports "no such module: fts5", and every FTS5
	// command then returned empty results instead of saying so.
	available bool
	docCount  int64
	documents map[string]*DocumentMetadata // In-memory fallback
}

// DocumentMetadata holds searchable text metadata.
type DocumentMetadata struct {
	MessageID      string
	Title          string
	Content        string
	Classification string
	Source         string
	Timestamp      time.Time
	Embedding      []float32
}

// FTS5SearchResult represents a full-text search result.
type FTS5SearchResult struct {
	MessageID      string
	Title          string
	Content        string
	Classification string
	Source         string
	Timestamp      time.Time
	Rank           float64 // BM25 rank
	Relevance      float64 // 0-100 relevance score
}

// NewFTS5Index creates a new full-text search index.
func NewFTS5Index(db *sql.DB) *FTS5Index {
	return &FTS5Index{
		db:        db,
		ready:     false,
		documents: make(map[string]*DocumentMetadata),
	}
}

// Initialize creates FTS5 virtual table and indexes.
// syncStatements keep documents_fts in step with the messages table.
//
// Triggers rather than calls at the Go call sites: messages are deleted from
// five places -- retention by id, by classification, by source and by age, plus
// DeleteMessage -- several running raw SQL inside transactions. A trigger
// covers all of them and cannot be forgotten by the next delete path someone
// adds. That matters most for removal: a message deleted for retention or
// classification reasons that stayed findable would be a leak, not a stale row.
//
// The timestamp is stored as epoch seconds because that is what IndexDocument
// writes (meta.Timestamp.Unix()) and what the readers scan.
var syncStatements = []string{
	`CREATE TRIGGER IF NOT EXISTS messages_fts_insert AFTER INSERT ON messages BEGIN
		INSERT INTO documents_fts(message_id, title, content, classification, source, timestamp)
		VALUES (new.id, COALESCE(new.conversation_title, ''), new.content,
		        new.classification, new.source,
		        COALESCE(CAST(strftime('%s', COALESCE(new.created_at, new.ingested_at)) AS INTEGER), 0));
	END`,
	`CREATE TRIGGER IF NOT EXISTS messages_fts_delete AFTER DELETE ON messages BEGIN
		DELETE FROM documents_fts WHERE message_id = old.id;
	END`,
	`CREATE TRIGGER IF NOT EXISTS messages_fts_update AFTER UPDATE ON messages BEGIN
		DELETE FROM documents_fts WHERE message_id = old.id;
		INSERT INTO documents_fts(message_id, title, content, classification, source, timestamp)
		VALUES (new.id, COALESCE(new.conversation_title, ''), new.content,
		        new.classification, new.source,
		        COALESCE(CAST(strftime('%s', COALESCE(new.created_at, new.ingested_at)) AS INTEGER), 0));
	END`,
	// Backfill: stores written before the triggers existed hold messages the
	// index has never seen, and nothing will update them again.
	`INSERT INTO documents_fts(message_id, title, content, classification, source, timestamp)
		SELECT m.id, COALESCE(m.conversation_title, ''), m.content, m.classification, m.source,
		       COALESCE(CAST(strftime('%s', COALESCE(m.created_at, m.ingested_at)) AS INTEGER), 0)
		FROM messages m
		WHERE NOT EXISTS (SELECT 1 FROM documents_fts f WHERE f.message_id = m.id)`,
}

// Initialize creates the FTS5 table, keeps it in step with messages, and
// backfills anything already stored.
//
// The index used to be created empty and left that way: ingestion never wrote
// to it, so `cadre knowledge fts5-search` returned nothing for content that had
// just been ingested, and only `fts5-index document add` could put anything in.
// An empty result is indistinguishable from no match, so the gap was silent.
//
// Maintained by triggers rather than by calls at the Go call sites. Messages
// are deleted from five different places -- retention sweeps by id, by
// classification, by source and by age, plus DeleteMessage -- several inside
// transactions running raw SQL. A trigger covers all of them and cannot be
// forgotten by the next delete path someone adds. That matters more for
// removal than for insertion: a message deleted for retention or
// classification reasons that stayed findable in the index would be a leak,
// not merely a stale row.
func (fi *FTS5Index) Initialize() error {
	fi.mu.Lock()
	defer fi.mu.Unlock()

	if fi.db == nil {
		// In-memory fallback, as before.
		fi.ready = true
		return nil
	}

	// Step one: the virtual table. If this fails the module is absent and
	// nothing else here can work.
	createTable := `CREATE VIRTUAL TABLE IF NOT EXISTS documents_fts USING fts5(
		message_id UNINDEXED,
		title,
		content,
		classification,
		source,
		timestamp UNINDEXED,
		tokenize = 'porter'
	)`
	if _, err := fi.db.Exec(createTable); err != nil {
		// No fts5 module compiled in. Fall back to the in-memory path rather
		// than failing the store open; Available() reports false so callers
		// refuse instead of returning an empty result set.
		fi.available = false
		fi.ready = true
		return nil
	}

	// Creating the table is not proof the module works. CREATE VIRTUAL TABLE
	// IF NOT EXISTS short-circuits on the name when the table is already
	// there, without resolving the module -- so a binary built *without*
	// -tags sqlite_fts5, opening a store some tagged binary created, saw the
	// CREATE succeed and reported FTS5 as available. It then answered "no
	// results" for content it could not read. Touching the table is what
	// actually requires the module.
	var probe int
	if err := fi.db.QueryRow("SELECT COUNT(*) FROM documents_fts").Scan(&probe); err != nil {
		fi.available = false
		fi.ready = true
		return nil
	}
	fi.available = true

	// Step two: keep it in step with messages, best effort.
	//
	// Deliberately not fatal and deliberately not part of Available(). This
	// index is also used directly, through IndexDocument, against databases
	// that have no messages table at all -- the hybrid searcher's own tests do
	// exactly that. A missing messages table means there is nothing to track,
	// not that full-text search is unavailable.
	for _, statement := range syncStatements {
		if _, err := fi.db.Exec(statement); err != nil {
			break
		}
	}

	fi.available = true
	fi.ready = true
	return nil
}

// Available reports whether this binary can use SQLite FTS5 at all.
//
// False means the sqlite3 driver was compiled without the fts5 module -- the
// default for mattn/go-sqlite3 unless built with `-tags sqlite_fts5`. Callers
// must refuse rather than return an empty result set, which is what every
// fts5 command did: indistinguishable from "no matches".
func (fi *FTS5Index) Available() bool {
	fi.mu.RLock()
	defer fi.mu.RUnlock()
	return fi.available
}

// IndexDocument adds or updates document in FTS5 index.
func (fi *FTS5Index) IndexDocument(meta *DocumentMetadata) error {
	fi.mu.Lock()
	defer fi.mu.Unlock()

	if !fi.ready {
		return fmt.Errorf("FTS5 index not initialized")
	}

	// Try SQLite if available
	if fi.db != nil {
		insertSQL := `
		INSERT INTO documents_fts(message_id, title, content, classification, source, timestamp)
		VALUES (?, ?, ?, ?, ?, ?)
		`

		result, err := fi.db.Exec(insertSQL,
			meta.MessageID,
			meta.Title,
			meta.Content,
			meta.Classification,
			meta.Source,
			meta.Timestamp.Unix(),
		)

		if err == nil {
			rowsAffected, err := result.RowsAffected()
			if err == nil && rowsAffected > 0 {
				fi.docCount++
			}
			return nil
		}
		// Fall through to in-memory if SQLite fails
	}

	// In-memory fallback
	if fi.documents[meta.MessageID] == nil {
		fi.docCount++
	}
	fi.documents[meta.MessageID] = meta

	return nil
}

// FullTextSearch performs FTS5 search.
func (fi *FTS5Index) FullTextSearch(query string, limit int) ([]FTS5SearchResult, error) {
	fi.mu.RLock()
	defer fi.mu.RUnlock()

	if !fi.ready {
		return nil, fmt.Errorf("FTS5 index not initialized")
	}

	// Try SQLite first
	if fi.db != nil {
		searchSQL := `
		SELECT message_id, title, content, classification, source, timestamp, rank
		FROM documents_fts
		WHERE documents_fts MATCH ?
		ORDER BY rank DESC
		LIMIT ?
		`

		rows, err := fi.db.Query(searchSQL, query, limit)
		if err == nil {
			var results []FTS5SearchResult
			for rows.Next() {
				var result FTS5SearchResult
				var timestamp int64
				var rank sql.NullFloat64

				err := rows.Scan(
					&result.MessageID,
					&result.Title,
					&result.Content,
					&result.Classification,
					&result.Source,
					&timestamp,
					&rank,
				)
				if err != nil {
					_ = rows.Close()
					return nil, fmt.Errorf("failed to scan result: %w", err)
				}

				result.Timestamp = time.Unix(timestamp, 0)
				if rank.Valid {
					result.Rank = rank.Float64
					result.Relevance = (rank.Float64 + 1.0) * 50.0 // Normalize to 0-100
				}

				results = append(results, result)
			}
			_ = rows.Close()
			return results, rows.Err()
		}
		// Fall through to in-memory if SQLite fails
	}

	// In-memory fallback: simple substring matching
	var results []FTS5SearchResult
	for _, doc := range fi.documents {
		if containsSubstring(doc.Title, query) || containsSubstring(doc.Content, query) {
			results = append(results, FTS5SearchResult{
				MessageID:      doc.MessageID,
				Title:          doc.Title,
				Content:        doc.Content,
				Classification: doc.Classification,
				Source:         doc.Source,
				Timestamp:      doc.Timestamp,
				Rank:           -1.0,
				Relevance:      75.0,
			})
		}
	}

	// Sort by timestamp (descending) for in-memory results
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Timestamp.After(results[i].Timestamp) {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	// Limit results
	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

// FilteredSearch performs FTS5 search with classification filter.
func (fi *FTS5Index) FilteredSearch(query, classification string, limit int) ([]FTS5SearchResult, error) {
	// An empty classification means "no classification filter", not
	// "classification equals the empty string".
	//
	// It used to mean the latter: the SQL appended `AND classification = ?`
	// unconditionally, so a hybrid search that named no classification matched
	// nothing at all. The in-memory fallback ignored the filter, and every
	// test ran against that fallback, so the SQLite path could return zero
	// results for a fully populated index without failing anything.
	//
	// Callers that must be explicit about classification are made to be at the
	// CLI boundary, where `cadre knowledge search` requires --classification;
	// this is a library filter, and an unset filter is not a filter.
	if classification == "" {
		return fi.FullTextSearch(query, limit)
	}

	fi.mu.RLock()
	defer fi.mu.RUnlock()

	if !fi.ready {
		return nil, fmt.Errorf("FTS5 index not initialized")
	}

	// Try SQLite first
	if fi.db != nil {
		searchSQL := `
		SELECT message_id, title, content, classification, source, timestamp, rank
		FROM documents_fts
		WHERE documents_fts MATCH ? AND classification = ?
		ORDER BY rank DESC
		LIMIT ?
		`

		rows, err := fi.db.Query(searchSQL, query, classification, limit)
		if err == nil {
			var results []FTS5SearchResult
			for rows.Next() {
				var result FTS5SearchResult
				var timestamp int64
				var rank sql.NullFloat64

				err := rows.Scan(
					&result.MessageID,
					&result.Title,
					&result.Content,
					&result.Classification,
					&result.Source,
					&timestamp,
					&rank,
				)
				if err != nil {
					_ = rows.Close()
					return nil, fmt.Errorf("failed to scan result: %w", err)
				}

				result.Timestamp = time.Unix(timestamp, 0)
				if rank.Valid {
					result.Rank = rank.Float64
					result.Relevance = (rank.Float64 + 1.0) * 50.0
				}

				results = append(results, result)
			}
			_ = rows.Close()
			return results, rows.Err()
		}
		// Fall through to in-memory if SQLite fails
	}

	// In-memory fallback
	var results []FTS5SearchResult
	for _, doc := range fi.documents {
		// Match classification if specified, otherwise include all
		classificationMatch := classification == "" || doc.Classification == classification

		if classificationMatch && (containsSubstring(doc.Title, query) || containsSubstring(doc.Content, query)) {
			results = append(results, FTS5SearchResult{
				MessageID:      doc.MessageID,
				Title:          doc.Title,
				Content:        doc.Content,
				Classification: doc.Classification,
				Source:         doc.Source,
				Timestamp:      doc.Timestamp,
				Rank:           -1.0,
				Relevance:      75.0,
			})
		}
	}

	// Sort and limit
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Timestamp.After(results[i].Timestamp) {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

// DeleteDocument removes document from FTS5 index.
func (fi *FTS5Index) DeleteDocument(messageID string) error {
	fi.mu.Lock()
	defer fi.mu.Unlock()

	if !fi.ready {
		return fmt.Errorf("FTS5 index not initialized")
	}

	// Try SQLite first
	if fi.db != nil {
		deleteSQL := `DELETE FROM documents_fts WHERE message_id = ?`
		result, err := fi.db.Exec(deleteSQL, messageID)
		if err == nil {
			rowsAffected, err := result.RowsAffected()
			if err == nil && rowsAffected > 0 {
				fi.docCount--
			}
			return nil
		}
		// Fall through to in-memory if SQLite fails
	}

	// In-memory fallback
	if fi.documents[messageID] != nil {
		delete(fi.documents, messageID)
		fi.docCount--
	}

	return nil
}

// GetDocumentCount returns total indexed documents.
func (fi *FTS5Index) GetDocumentCount() int64 {
	fi.mu.RLock()
	defer fi.mu.RUnlock()

	// Queried, not remembered. This returned fi.docCount, a counter that only
	// IndexDocument incremented -- so with the index maintained by triggers, and
	// on any freshly opened store, it reported 0 documents over a fully
	// populated index.
	if fi.available && fi.db != nil {
		var count int64
		if err := fi.db.QueryRow("SELECT COUNT(*) FROM documents_fts").Scan(&count); err == nil {
			return count
		}
	}
	return fi.docCount
}

// The HybridSearcher that stood here is gone, with the HNSW index it combined.
//
// It fused vector results from HSNWIndex with text results from this index. The
// vector half never worked: nothing in any binary could build an HSNWIndex, so
// every method on it was unreachable, and `cadre knowledge hybrid-search
// vector-only` said as much by telling operators to initialise an index no
// command could create. A "hybrid" searcher with one working half is a text
// searcher with extra arithmetic.
//
// The CLI's hybrid-search subcommands never used it: they call FTS5Index
// directly, which is what actually searches.
func containsSubstring(text, query string) bool {
	textLower := ""
	queryLower := ""

	for _, c := range text {
		if c >= 'A' && c <= 'Z' {
			textLower += string(c + 32)
		} else {
			textLower += string(c)
		}
	}

	for _, c := range query {
		if c >= 'A' && c <= 'Z' {
			queryLower += string(c + 32)
		} else {
			queryLower += string(c)
		}
	}

	// Simple substring match
	for i := 0; i <= len(textLower)-len(queryLower); i++ {
		match := true
		for j := 0; j < len(queryLower); j++ {
			if textLower[i+j] != queryLower[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// powFloat calculates non-linear weighting.
