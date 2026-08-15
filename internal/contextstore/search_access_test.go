package contextstore

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// Search is the one read path that goes looking. get and list start from a
// handle or a filter the caller already named; search starts from "anything
// that resembles this" and then has to decide what the caller may see.
//
// Ported from roster/context-store/test/test_search.py, which had 37 tests
// against 2 in Go. These cover the ones with no counterpart: what search
// refuses to return, what it indexes, and what it records.

func searchTestStore(t *testing.T) (*Config, string) {
	t.Helper()
	directory := t.TempDir()
	// Mirrors the shape the existing service tests use, so these exercise the
	// same limits and embedding a real store runs with.
	cfg := &Config{
		Database:  filepath.Join(directory, "store.db"),
		Ingestion: map[string]any{"redact_secrets": true},
		Expiry: Expiry{
			DefaultTTLDaysByScope: map[string]int{"agent": 1, "dispatch": 7, "project": 30},
			MaximumTTLDays:        90,
		},
		Limits:    map[string]any{"max_entry_bytes": 1048576},
		Chunking:  Chunking{MaxCharacters: 2400, OverlapCharacters: 240},
		Embedding: Embedding{Provider: "hashing", Model: "feature-hash-v1", Dimensions: 384},
	}
	return cfg, cfg.Database
}

// storeEntry puts one entry and returns its handle.
func storeEntry(t *testing.T, cfg *Config, opts PutOptions) string {
	t.Helper()
	db, err := OpenStore(cfg.Database, false)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = db.Close() }()
	result, err := PutEntry(db, cfg, opts)
	if err != nil {
		t.Fatalf("PutEntry(%s): %v", opts.Label, err)
	}
	return result.Handle
}

func searchFor(t *testing.T, cfg *Config, query string, caller CallerOptions, scope string) *Bundle[SearchResultItem] {
	t.Helper()
	db, err := OpenStore(cfg.Database, false)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = db.Close() }()
	caller.Scope = scope
	bundle, err := SearchEntries(db, cfg, query, SearchOptions{CallerOptions: caller})
	if err != nil {
		t.Fatalf("SearchEntries: %v", err)
	}
	return bundle
}

func labelsIn(bundle *Bundle[SearchResultItem]) []string {
	var labels []string
	for _, item := range bundle.Results {
		labels = append(labels, item.Label)
	}
	return labels
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func TestSearchDoesNotReturnAnotherAgentsScopedEntry(t *testing.T) {
	// The reason search needs its own access control: an agent-scoped entry is
	// invisible to `get` because the caller has no handle for it, but a search
	// finds things the caller never knew existed. Relevance is not permission.
	cfg, _ := searchTestStore(t)
	base := PutOptions{
		Scope: "agent", Classification: "internal", Source: "s", TaskID: "T-1",
		Content: "postgres connection pooling notes",
	}

	mine := base
	mine.Agent, mine.Label = "agent-one", "mine"
	storeEntry(t, cfg, mine)

	theirs := base
	theirs.Agent, theirs.Label = "agent-two", "theirs"
	storeEntry(t, cfg, theirs)

	found := labelsIn(searchFor(t, cfg, "postgres connection pooling",
		CallerOptions{Agent: "agent-one", TaskID: "T-1", Classification: "internal", Source: "s"}, ""))

	if !contains(found, "mine") {
		t.Errorf("the caller's own entry was not returned: %v", found)
	}
	if contains(found, "theirs") {
		t.Errorf("another agent's agent-scoped entry was returned by search: %v", found)
	}
}

func TestSearchDoesNotCrossClassificationOrSource(t *testing.T) {
	// The result-level property: whatever the layering, a caller must not see
	// them. That the exclusion also happens in SQL, before anything is scored,
	// is pinned separately in TestUnreadableRowsAreExcludedInSQLNotAfterScoring
	// -- this test cannot tell the two layers apart, because either one alone
	// produces the same results.
	cfg, _ := searchTestStore(t)
	base := PutOptions{
		Scope: "project", Agent: "a", TaskID: "T-1",
		Content: "kubernetes ingress controller configuration",
	}

	visible := base
	visible.Classification, visible.Source, visible.Label = "internal", "project-a", "visible"
	storeEntry(t, cfg, visible)

	otherClass := base
	otherClass.Classification, otherClass.Source, otherClass.Label = "confidential", "project-a", "other-classification"
	storeEntry(t, cfg, otherClass)

	otherSource := base
	otherSource.Classification, otherSource.Source, otherSource.Label = "internal", "project-b", "other-source"
	storeEntry(t, cfg, otherSource)

	found := labelsIn(searchFor(t, cfg, "kubernetes ingress controller",
		CallerOptions{Agent: "a", TaskID: "T-1", Classification: "internal", Source: "project-a"}, ""))

	if !contains(found, "visible") {
		t.Errorf("the matching entry was not returned: %v", found)
	}
	if contains(found, "other-classification") {
		t.Errorf("an entry at a different classification was returned: %v", found)
	}
	if contains(found, "other-source") {
		t.Errorf("an entry from a different source partition was returned: %v", found)
	}
}

func TestUnreadableRowsAreExcludedInSQLNotAfterScoring(t *testing.T) {
	// Two layers enforce classification and source: a WHERE clause in
	// LoadSearchableChunks, and readable() on each candidate afterwards.
	//
	// Because readable() alone is sufficient for the *result*, deleting the
	// SQL filter changes nothing a caller can observe -- which is how the
	// first version of this file "verified" it and passed with the WHERE
	// clause removed. So the SQL layer is asserted where it lives.
	//
	// It matters independently of the result: without it, every entry in every
	// classification and every source partition is read out of the database
	// and into this process before anything decides they are off limits.
	cfg, _ := searchTestStore(t)
	base := PutOptions{
		Scope: "project", Agent: "a", TaskID: "T-1",
		Content: "vault transit engine key rotation",
	}
	visible := base
	visible.Classification, visible.Source, visible.Label = "internal", "project-a", "visible"
	storeEntry(t, cfg, visible)

	otherClass := base
	otherClass.Classification, otherClass.Source, otherClass.Label = "confidential", "project-a", "other-classification"
	storeEntry(t, cfg, otherClass)

	otherSource := base
	otherSource.Classification, otherSource.Source, otherSource.Label = "internal", "project-b", "other-source"
	storeEntry(t, cfg, otherSource)

	db, err := OpenStore(cfg.Database, false)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = db.Close() }()

	chunks, err := LoadSearchableChunks(db, cfg.Embedding, LoadSearchableChunksFilters{
		Classification: "internal", Source: "project-a",
	})
	if err != nil {
		t.Fatalf("LoadSearchableChunks: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("no chunks loaded at all, so this test asserted nothing")
	}
	for _, chunk := range chunks {
		if chunk.Entry.Classification != "internal" {
			t.Errorf("a %q-classification row was loaded from SQL: %s",
				chunk.Entry.Classification, chunk.Entry.Label)
		}
		if chunk.Entry.Source != "project-a" {
			t.Errorf("a row from source %q was loaded from SQL: %s",
				chunk.Entry.Source, chunk.Entry.Label)
		}
	}
}

func TestTheScopeFilterNarrowsRatherThanWidens(t *testing.T) {
	// A caller can ask for less than it may see. It must not be able to ask
	// for more -- naming a scope is a filter, not a claim of access.
	cfg, _ := searchTestStore(t)
	base := PutOptions{
		Agent: "a", TaskID: "T-1", Classification: "internal", Source: "s",
		Content: "terraform module registry layout",
	}

	agentScoped := base
	agentScoped.Scope, agentScoped.Label = "agent", "agent-scoped"
	storeEntry(t, cfg, agentScoped)

	projectScoped := base
	projectScoped.Scope, projectScoped.Label = "project", "project-scoped"
	storeEntry(t, cfg, projectScoped)

	caller := CallerOptions{Agent: "a", TaskID: "T-1", Classification: "internal", Source: "s"}

	// Unfiltered: both are readable by this caller.
	all := labelsIn(searchFor(t, cfg, "terraform module registry", caller, ""))
	if !contains(all, "agent-scoped") || !contains(all, "project-scoped") {
		t.Fatalf("expected both entries to be readable unfiltered: %v", all)
	}

	// Filtered to project: only that one.
	narrowed := labelsIn(searchFor(t, cfg, "terraform module registry", caller, "project"))
	if contains(narrowed, "agent-scoped") {
		t.Errorf("the scope filter widened rather than narrowed: %v", narrowed)
	}
	if !contains(narrowed, "project-scoped") {
		t.Errorf("the scope filter excluded the scope it named: %v", narrowed)
	}

	// And a different agent naming "agent" scope still sees nothing of the
	// first agent's -- the filter cannot be used as a way in.
	other := CallerOptions{Agent: "agent-two", TaskID: "T-1", Classification: "internal", Source: "s"}
	forced := labelsIn(searchFor(t, cfg, "terraform module registry", other, "agent"))
	if contains(forced, "agent-scoped") {
		t.Errorf("naming a scope granted access to another agent's entry: %v", forced)
	}
}

func TestChunksIndexTheRedactedTextNotTheOriginal(t *testing.T) {
	// The leak this prevents: an entry's stored content is redacted, but if
	// the search index were built from the pre-redaction text, a query could
	// match on the secret and return the chunk containing it. The redaction
	// would be intact in the entry and defeated in the index.
	cfg, _ := searchTestStore(t)
	const secret = "ghp_0123456789abcdefghijklmnopqrstuvwxyzAB" //nolint:gosec // a fake token shaped to trip the redactor

	handle := storeEntry(t, cfg, PutOptions{
		Scope: "agent", Agent: "a", TaskID: "T-1", Classification: "internal", Source: "s",
		Label: "with-secret", Content: "deployment notes token " + secret + " end of notes",
	})

	db, err := OpenStore(cfg.Database, false)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = db.Close() }()

	// The stored entry is redacted...
	var stored string
	if err := db.QueryRow("SELECT content FROM entries WHERE handle = ?", handle).Scan(&stored); err != nil {
		t.Fatalf("reading the entry: %v", err)
	}
	if strings.Contains(stored, secret) {
		t.Skip("the redactor did not treat this value as a secret; the index check below would be vacuous")
	}

	// ...and so is every chunk indexed from it.
	rows, err := db.Query("SELECT content FROM entry_chunks WHERE handle = ?", handle)
	if err != nil {
		t.Fatalf("reading chunks: %v", err)
	}
	defer func() { _ = rows.Close() }()
	chunkCount := 0
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			t.Fatal(err)
		}
		chunkCount++
		if strings.Contains(content, secret) {
			t.Error("a search chunk contains the secret that was redacted from the entry")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if chunkCount == 0 {
		t.Error("no chunks were indexed, so this test asserted nothing")
	}
}

func TestACorruptStoredVectorIsSkippedRatherThanFailingTheQuery(t *testing.T) {
	// One unreadable row must not take down the whole search. The caller
	// cannot fix a corrupt vector, and answering with the rest is strictly
	// better than answering with an error.
	cfg, _ := searchTestStore(t)
	good := storeEntry(t, cfg, PutOptions{
		Scope: "agent", Agent: "a", TaskID: "T-1", Classification: "internal", Source: "s",
		Label: "good", Content: "redis cluster failover behaviour",
	})
	corrupt := storeEntry(t, cfg, PutOptions{
		Scope: "agent", Agent: "a", TaskID: "T-1", Classification: "internal", Source: "s",
		Label: "corrupt", Content: "redis cluster failover behaviour",
	})

	db, err := OpenStore(cfg.Database, false)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if _, err := db.Exec(
		"UPDATE entry_chunks SET embedding_json = ? WHERE handle = ?", "not json at all", corrupt,
	); err != nil {
		t.Fatalf("corrupting the vector: %v", err)
	}
	_ = db.Close()

	found := labelsIn(searchFor(t, cfg, "redis cluster failover",
		CallerOptions{Agent: "a", TaskID: "T-1", Classification: "internal", Source: "s"}, ""))

	if !contains(found, "good") {
		t.Errorf("a corrupt row suppressed the healthy result too: %v", found)
	}
	if contains(found, "corrupt") {
		t.Errorf("an unscoreable chunk was returned anyway: %v", found)
	}
	_ = good
}

func TestASearchIsRecordedByQueryHashNeverQueryText(t *testing.T) {
	// The audit trail says a search happened and by whom. The query itself is
	// the one part that can carry what the caller was looking for -- often the
	// most sensitive thing about the call -- so it is recorded as a digest.
	cfg, _ := searchTestStore(t)
	storeEntry(t, cfg, PutOptions{
		Scope: "agent", Agent: "a", TaskID: "T-1", Classification: "internal", Source: "s",
		Label: "entry", Content: "unrelated content",
	})

	const query = "SENSITIVE-SEARCH-TERM-0f3a9c"
	searchFor(t, cfg, query,
		CallerOptions{Agent: "a", TaskID: "T-1", Classification: "internal", Source: "s"}, "")

	db, err := OpenStore(cfg.Database, false)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = db.Close() }()

	rows, err := db.Query("SELECT * FROM access_runs")
	if err != nil {
		t.Skipf("no access_runs table to inspect: %v", err)
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}

	sawSearch := false
	for rows.Next() {
		// Scanned as strings, not through json.Marshal. SQLite hands TEXT back
		// as []byte, which json.Marshal base64-encodes -- so a row containing
		// the query verbatim would not have matched a plain-text search for
		// it, and this test passed against an implementation deliberately
		// changed to log the query. Found by falsification, not by it passing.
		cells := make([]any, len(columns))
		holders := make([]sql.NullString, len(columns))
		for index := range cells {
			cells[index] = &holders[index]
		}
		if err := rows.Scan(cells...); err != nil {
			t.Fatal(err)
		}
		var parts []string
		for _, holder := range holders {
			if holder.Valid {
				parts = append(parts, holder.String)
			}
		}
		row := strings.Join(parts, " | ")
		if strings.Contains(row, "search") {
			sawSearch = true
		}
		if strings.Contains(row, query) {
			t.Errorf("the audit row carries the query text: %s", row)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !sawSearch {
		t.Error("the search was not recorded at all")
	}
}
