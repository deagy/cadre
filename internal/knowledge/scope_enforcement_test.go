package knowledge

import (
	"strings"
	"testing"
)

// Scope and classification enforcement for knowledge-store retrieval.
//
// These are the Go guards for the fail-closed guarantees documented in
// roster/knowledge-store/SECURITY.md ("Global default, project-local
// override", "Retrieval rules", "Known limitations"). They replace the
// coverage that roster/knowledge-store/test/test_scope_enforcement.py gave
// the deleted Python implementation -- rewritten against this package's own
// API rather than translated statement by statement, because the two
// implementations are not a line-for-line port.
//
// What these tests do NOT establish, stated plainly because SECURITY.md
// insists on it: source, classification, agent and task are caller-asserted
// and authenticated by nobody. Every assertion below is about filters being
// applied honestly to an honest caller, never about an access-control
// boundary that would survive a dishonest one.
//
// Requires the cgo SQLite driver; see sqlite_guard_test.go.

// seedScopedCorpus writes one embedded message per (source, classification)
// pair so a retrieval's scope can be observed by which rows come back.
func seedScopedCorpus(t *testing.T, store *Store, embedder EmbeddingProvider, rows []struct {
	source         string
	classification string
	body           string
}) {
	t.Helper()
	for i, row := range rows {
		msgID, err := store.SaveMessage(
			row.source, nil, "conv-"+row.source, nil, row.body,
			"user", row.body, nil, row.classification, false,
			`[]`, `{}`, nil,
		)
		if err != nil {
			t.Fatalf("SaveMessage(%d): %v", i, err)
		}
		vecs, err := embedder.Embed([]string{row.body})
		if err != nil {
			t.Fatalf("Embed(%d): %v", i, err)
		}
		if err := store.SaveChunk(msgID, 0, row.body, embedder.Name(), embedder.Model(), vecs[0]); err != nil {
			t.Fatalf("SaveChunk(%d): %v", i, err)
		}
	}
}

func twoSourceCorpus(t *testing.T) (*Store, EmbeddingProvider) {
	t.Helper()
	store := setupTestDB(t)
	t.Cleanup(func() { _ = store.Close() })
	embedder := NewLocalHashingEmbedder(128)
	seedScopedCorpus(t, store, embedder, []struct {
		source         string
		classification string
		body           string
	}{
		{"project-alpha", "internal", "alpha deployment runbook"},
		{"project-beta", "internal", "beta deployment runbook"},
	})
	return store, embedder
}

// --- Source scope must be an explicit caller choice ------------------------

// TestSearchRefusesAnUnscopedRead is the load-bearing one. The knowledge
// store defaults to a single shared database for every project without its
// own partition, so omitting a source filter is not "no filter" -- it is a
// cross-project read. It must be refused, not inferred.
func TestSearchRefusesAnUnscopedRead(t *testing.T) {
	requireSQLite(t)
	store, embedder := twoSourceCorpus(t)

	results, err := store.Search(SearchOptions{
		Query:             "deployment runbook",
		Classification:    "internal",
		EmbeddingProvider: embedder,
		// No SourceFilters, AllSources not set: scope left to inference.
	})
	if err == nil {
		t.Fatalf("expected a refusal for an unscoped read, got %d results", len(results))
	}
	if results != nil {
		t.Errorf("a refused retrieval must return no results, got %d", len(results))
	}
	if !strings.Contains(err.Error(), "source scope is required") {
		t.Errorf("error should name the missing scope, got: %v", err)
	}
}

// TestSearchRefusesAmbiguousScope pins the other half: a caller that both
// names sources and asks for all of them has not made a choice, and
// resolving it silently (either way) would be a guess.
func TestSearchRefusesAmbiguousScope(t *testing.T) {
	requireSQLite(t)
	store, embedder := twoSourceCorpus(t)

	_, err := store.Search(SearchOptions{
		Query:             "deployment runbook",
		Classification:    "internal",
		SourceFilters:     []string{"project-alpha"},
		AllSources:        true,
		EmbeddingProvider: embedder,
	})
	if err == nil {
		t.Fatal("expected a refusal when both a source filter and all-sources are supplied")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error should name the ambiguity, got: %v", err)
	}
}

// TestSearchRefusesABlankSourceFilterEntry closes the fail-open retype:
// a blank entry must not be accepted and then quietly matched against
// nothing (or, worse, dropped so the read becomes unscoped).
func TestSearchRefusesABlankSourceFilterEntry(t *testing.T) {
	requireSQLite(t)
	store, embedder := twoSourceCorpus(t)

	for _, filters := range [][]string{{""}, {"   "}, {"project-alpha", ""}} {
		_, err := store.Search(SearchOptions{
			Query:             "deployment runbook",
			Classification:    "internal",
			SourceFilters:     filters,
			EmbeddingProvider: embedder,
		})
		if err == nil {
			t.Errorf("expected a refusal for source filters %q", filters)
		}
	}
}

// TestSearchWithASourceFilterExcludesEveryOtherSource is the positive case
// with its negative attached: naming one source must not merely rank the
// others lower, it must exclude them.
func TestSearchWithASourceFilterExcludesEveryOtherSource(t *testing.T) {
	requireSQLite(t)
	store, embedder := twoSourceCorpus(t)

	results, err := store.Search(SearchOptions{
		Query:             "deployment runbook",
		Classification:    "internal",
		SourceFilters:     []string{"project-alpha"},
		EmbeddingProvider: embedder,
		Top:               20,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected exactly the one in-scope message, got %d", len(results))
	}
	if results[0].Message.Source != "project-alpha" {
		t.Errorf("source = %q, want project-alpha", results[0].Message.Source)
	}
	for _, r := range results {
		if r.Message.Source == "project-beta" {
			t.Fatal("a scoped read returned another project's content")
		}
	}
}

// TestSearchAllSourcesIsAnExplicitOptIn: the wide read is available, but
// only because the caller asked for it by name.
func TestSearchAllSourcesIsAnExplicitOptIn(t *testing.T) {
	requireSQLite(t)
	store, embedder := twoSourceCorpus(t)

	results, err := store.Search(SearchOptions{
		Query:             "deployment runbook",
		Classification:    "internal",
		AllSources:        true,
		EmbeddingProvider: embedder,
		Top:               20,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	seen := map[string]bool{}
	for _, r := range results {
		seen[r.Message.Source] = true
	}
	if !seen["project-alpha"] || !seen["project-beta"] {
		t.Errorf("all-sources should span both sources, saw %v", seen)
	}
}

// TestSearchWithSeveralSourceFiltersSpansExactlyThose: a caller may widen
// deliberately to a named set, and the set is still a boundary.
func TestSearchWithSeveralSourceFiltersSpansExactlyThose(t *testing.T) {
	requireSQLite(t)
	store := setupTestDB(t)
	defer func() { _ = store.Close() }()
	embedder := NewLocalHashingEmbedder(128)
	seedScopedCorpus(t, store, embedder, []struct {
		source         string
		classification string
		body           string
	}{
		{"project-alpha", "internal", "alpha runbook"},
		{"project-beta", "internal", "beta runbook"},
		{"project-gamma", "internal", "gamma runbook"},
	})

	results, err := store.Search(SearchOptions{
		Query:             "runbook",
		Classification:    "internal",
		SourceFilters:     []string{"project-alpha", "project-gamma"},
		EmbeddingProvider: embedder,
		Top:               20,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, r := range results {
		if r.Message.Source == "project-beta" {
			t.Fatal("an unnamed source was returned")
		}
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results from the two named sources, got %d", len(results))
	}
}

// --- Classification ---------------------------------------------------------

// TestSearchClassificationIsExactMatchNotHierarchical pins SECURITY.md's
// "Classification filtering is exact-match, not hierarchical". A caller
// asserting `internal` must not reach `confidential` or `restricted`
// content, and must not reach `public` either -- exact means exact in both
// directions, which is the part that is easy to "helpfully" break.
func TestSearchClassificationIsExactMatchNotHierarchical(t *testing.T) {
	requireSQLite(t)
	store := setupTestDB(t)
	defer func() { _ = store.Close() }()
	embedder := NewLocalHashingEmbedder(128)
	seedScopedCorpus(t, store, embedder, []struct {
		source         string
		classification string
		body           string
	}{
		{"project-alpha", "public", "alpha public note"},
		{"project-alpha", "internal", "alpha internal note"},
		{"project-alpha", "confidential", "alpha confidential note"},
		{"project-alpha", "restricted", "alpha restricted note"},
	})

	for _, asserted := range []string{"public", "internal", "confidential", "restricted"} {
		results, err := store.Search(SearchOptions{
			Query:             "alpha note",
			Classification:    asserted,
			SourceFilters:     []string{"project-alpha"},
			EmbeddingProvider: embedder,
			Top:               20,
		})
		if err != nil {
			t.Fatalf("Search(%s): %v", asserted, err)
		}
		if len(results) != 1 {
			t.Fatalf("Search(%s) returned %d results, want exactly the same-classification one",
				asserted, len(results))
		}
		if got := results[0].Message.Classification; got != asserted {
			t.Errorf("Search(%s) returned classification %q", asserted, got)
		}
	}
}

// TestSearchClassificationIsNotFuzzyMatched: no case folding, no trimming,
// no prefix behaviour. A near-miss is a miss, not a widening.
func TestSearchClassificationIsNotFuzzyMatched(t *testing.T) {
	requireSQLite(t)
	store := setupTestDB(t)
	defer func() { _ = store.Close() }()
	embedder := NewLocalHashingEmbedder(128)
	seedScopedCorpus(t, store, embedder, []struct {
		source         string
		classification string
		body           string
	}{
		{"project-alpha", "restricted", "alpha restricted note"},
	})

	for _, nearMiss := range []string{"RESTRICTED", "Restricted", "restricted ", " restricted", "restrict", "%"} {
		results, err := store.Search(SearchOptions{
			Query:             "alpha note",
			Classification:    nearMiss,
			SourceFilters:     []string{"project-alpha"},
			EmbeddingProvider: embedder,
			Top:               20,
		})
		if err != nil {
			t.Fatalf("Search(%q): %v", nearMiss, err)
		}
		if len(results) != 0 {
			t.Errorf("classification %q matched %d restricted rows; exact-match means exact",
				nearMiss, len(results))
		}
	}
}

// TestSearchFailsClosedOnMissingCallerInputs: every required input is
// refused when absent rather than defaulted. SECURITY.md: retrieval
// "requires explicit agent/task/classification and fails closed on missing
// config".
func TestSearchFailsClosedOnMissingCallerInputs(t *testing.T) {
	requireSQLite(t)
	store, embedder := twoSourceCorpus(t)

	cases := []struct {
		name string
		opts SearchOptions
	}{
		{"no query", SearchOptions{
			Classification: "internal", AllSources: true, EmbeddingProvider: embedder}},
		{"no classification", SearchOptions{
			Query: "runbook", AllSources: true, EmbeddingProvider: embedder}},
		{"no embedding provider", SearchOptions{
			Query: "runbook", Classification: "internal", AllSources: true}},
		{"no scope", SearchOptions{
			Query: "runbook", Classification: "internal", EmbeddingProvider: embedder}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := store.Search(tc.opts); err == nil {
				t.Errorf("expected a refusal with %s", tc.name)
			}
		})
	}
}

// --- Embedding identity -----------------------------------------------------

// TestSearchExcludesChunksEmbeddedByADifferentModel pins SECURITY.md's
// "Re-ingest after provider/model/dimension changes ... mismatched stored
// dimensions are excluded". Scoring an incomparable vector as 0.0 and
// letting top-k decide is not exclusion: on a sparse store it still
// surfaces the content.
func TestSearchExcludesChunksEmbeddedByADifferentModel(t *testing.T) {
	requireSQLite(t)
	store := setupTestDB(t)
	defer func() { _ = store.Close() }()

	embedder := NewLocalHashingEmbedder(128)
	msgID, err := store.SaveMessage(
		"project-alpha", nil, "conv-1", nil, "msg-1",
		"user", "alpha runbook", nil, "internal", false, `[]`, `{}`, nil,
	)
	if err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	vecs, err := embedder.Embed([]string{"alpha runbook"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	// Same provider name, a different model identity.
	if err := store.SaveChunk(msgID, 0, "alpha runbook", embedder.Name(), "some-other-model-v2", vecs[0]); err != nil {
		t.Fatalf("SaveChunk: %v", err)
	}

	results, err := store.Search(SearchOptions{
		Query:             "alpha runbook",
		Classification:    "internal",
		SourceFilters:     []string{"project-alpha"},
		EmbeddingProvider: embedder,
		Top:               20,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("a chunk embedded by a different model was returned (%d results); "+
			"vectors from different models are not comparable", len(results))
	}

	// Positive control: the same message, chunked under the querying
	// embedder's own identity, must come back. Without this the assertion
	// above would also pass if search were simply broken.
	if err := store.SaveChunk(msgID, 1, "alpha runbook", embedder.Name(), embedder.Model(), vecs[0]); err != nil {
		t.Fatalf("SaveChunk (control): %v", err)
	}
	results, err = store.Search(SearchOptions{
		Query:             "alpha runbook",
		Classification:    "internal",
		SourceFilters:     []string{"project-alpha"},
		EmbeddingProvider: embedder,
		Top:               20,
	})
	if err != nil {
		t.Fatalf("Search (control): %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("positive control returned %d results, want 1", len(results))
	}
}

// TestSearchExcludesChunksOfADifferentDimension is the same guarantee on the
// axis SECURITY.md names explicitly.
func TestSearchExcludesChunksOfADifferentDimension(t *testing.T) {
	requireSQLite(t)
	store := setupTestDB(t)
	defer func() { _ = store.Close() }()

	shortEmbedder := NewLocalHashingEmbedder(64)
	querying := NewLocalHashingEmbedder(128)

	msgID, err := store.SaveMessage(
		"project-alpha", nil, "conv-1", nil, "msg-1",
		"user", "alpha runbook", nil, "internal", false, `[]`, `{}`, nil,
	)
	if err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	vecs, err := shortEmbedder.Embed([]string{"alpha runbook"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs[0]) != 64 {
		t.Fatalf("expected a 64-dimension stored vector, got %d", len(vecs[0]))
	}
	// Deliberately label the short vector with the querying embedder's own
	// provider and model identity, so provider/model equality cannot be what
	// excludes it -- only the dimension differs. This is exactly the
	// "reused model name" hazard SECURITY.md warns the demo cannot
	// distinguish by name alone.
	if err := store.SaveChunk(msgID, 0, "alpha runbook", querying.Name(), querying.Model(), vecs[0]); err != nil {
		t.Fatalf("SaveChunk: %v", err)
	}

	results, err := store.Search(SearchOptions{
		Query:             "alpha runbook",
		Classification:    "internal",
		SourceFilters:     []string{"project-alpha"},
		EmbeddingProvider: querying,
		Top:               20,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("a 64-dimension chunk was returned to a 128-dimension query (%d results)", len(results))
	}

	// Positive control: a matching-dimension chunk under the same
	// provider/model identity does come back, so the exclusion above is the
	// dimension check and not a broken query.
	full, err := querying.Embed([]string{"alpha runbook"})
	if err != nil {
		t.Fatalf("Embed (control): %v", err)
	}
	if err := store.SaveChunk(msgID, 1, "alpha runbook", querying.Name(), querying.Model(), full[0]); err != nil {
		t.Fatalf("SaveChunk (control): %v", err)
	}
	results, err = store.Search(SearchOptions{
		Query:             "alpha runbook",
		Classification:    "internal",
		SourceFilters:     []string{"project-alpha"},
		EmbeddingProvider: querying,
		Top:               20,
	})
	if err != nil {
		t.Fatalf("Search (control): %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("positive control returned %d results, want 1", len(results))
	}
}

// --- Retrieval audit --------------------------------------------------------

// TestRetrievalAuditRecordsScopeAndAttributionNeverTheRawQuery pins the
// audit contract from SECURITY.md's "Retrieval rules": the demo records
// query hash, task ID, agent, caller-supplied classification/source filter,
// embedding provider/model, requested top, result count and time --
// explicitly "without the raw query".
func TestRetrievalAuditRecordsScopeAndAttributionNeverTheRawQuery(t *testing.T) {
	requireSQLite(t)
	store, embedder := twoSourceCorpus(t)

	const rawQuery = "an unusually distinctive deployment runbook phrase"
	if _, err := store.Search(SearchOptions{
		Query:             rawQuery,
		Classification:    "internal",
		SourceFilters:     []string{"project-alpha"},
		Agent:             "retrieval-pipeline-implementer",
		TaskID:            "TASK-77",
		EmbeddingProvider: embedder,
		Top:               3,
	}); err != nil {
		t.Fatalf("Search: %v", err)
	}

	var queryHash, taskID, agent, classification, sourceFilter, provider, model string
	var requestedTop, resultCount int
	err := store.db.QueryRow(`
		SELECT query_hash, task_id, agent, classification, source_filter,
		       embedding_provider, embedding_model, requested_top, result_count
		FROM retrieval_runs
	`).Scan(&queryHash, &taskID, &agent, &classification, &sourceFilter,
		&provider, &model, &requestedTop, &resultCount)
	if err != nil {
		t.Fatalf("reading the audit row: %v", err)
	}

	if queryHash == rawQuery || strings.Contains(queryHash, "runbook") {
		t.Errorf("the audit row stored the raw query: %q", queryHash)
	}
	if queryHash != hashQueryString(rawQuery) {
		t.Errorf("query_hash = %q, want the query's hash", queryHash)
	}
	if agent != "retrieval-pipeline-implementer" {
		t.Errorf("agent = %q; caller attribution must be recorded as supplied", agent)
	}
	if taskID != "TASK-77" {
		t.Errorf("task_id = %q; caller attribution must be recorded as supplied", taskID)
	}
	if classification != "internal" {
		t.Errorf("classification = %q", classification)
	}
	if sourceFilter != "project-alpha" {
		t.Errorf("source_filter = %q; the scope the read ran under must be on the record", sourceFilter)
	}
	if provider != embedder.Name() || model != embedder.Model() {
		t.Errorf("embedding identity = %q/%q, want %q/%q", provider, model, embedder.Name(), embedder.Model())
	}
	if requestedTop != 3 {
		t.Errorf("requested_top = %d, want 3", requestedTop)
	}
	if resultCount != 1 {
		t.Errorf("result_count = %d, want 1", resultCount)
	}

	// Nothing else in the row may carry the query text either.
	var rowText string
	if err := store.db.QueryRow(
		`SELECT group_concat(query_hash || '|' || task_id || '|' || agent || '|' || source_filter)
		 FROM retrieval_runs`).Scan(&rowText); err != nil {
		t.Fatalf("re-reading the audit row: %v", err)
	}
	if strings.Contains(rowText, "distinctive") {
		t.Errorf("audit row leaked query text: %q", rowText)
	}
}

// TestRetrievalAuditDistinguishesAWideReadFromANamedSource: an all-sources
// read must not be recorded as though it had been scoped.
func TestRetrievalAuditDistinguishesAWideReadFromANamedSource(t *testing.T) {
	requireSQLite(t)
	store, embedder := twoSourceCorpus(t)

	if _, err := store.Search(SearchOptions{
		Query:             "runbook",
		Classification:    "internal",
		AllSources:        true,
		Agent:             "code-reviewer",
		TaskID:            "TASK-78",
		EmbeddingProvider: embedder,
	}); err != nil {
		t.Fatalf("Search: %v", err)
	}

	var sourceFilter string
	if err := store.db.QueryRow(
		`SELECT source_filter FROM retrieval_runs`).Scan(&sourceFilter); err != nil {
		t.Fatalf("reading the audit row: %v", err)
	}
	if sourceFilter != "" {
		t.Errorf("source_filter = %q; an all-sources read ran under no source filter "+
			"and must not be recorded as though it named one", sourceFilter)
	}
}

// TestRefusedRetrievalIsNotAudited: a refusal never happened, so it must not
// leave a retrieval_runs row implying a read took place.
func TestRefusedRetrievalIsNotAudited(t *testing.T) {
	requireSQLite(t)
	store, embedder := twoSourceCorpus(t)

	if _, err := store.Search(SearchOptions{
		Query:             "runbook",
		Classification:    "internal",
		EmbeddingProvider: embedder,
	}); err == nil {
		t.Fatal("expected the unscoped read to be refused")
	}

	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM retrieval_runs`).Scan(&count); err != nil {
		t.Fatalf("counting audit rows: %v", err)
	}
	if count != 0 {
		t.Errorf("a refused retrieval wrote %d audit row(s)", count)
	}
}

// --- Deletion evidence ------------------------------------------------------

// TestDeletionEvidencePrecedesAndOutlivesTheContent pins the ordering
// SECURITY.md describes: evidence is written and committed first, so an
// interrupted deletion is still on the record, and the evidence row outlives
// the content it describes.
func TestDeletionEvidencePrecedesAndOutlivesTheContent(t *testing.T) {
	requireSQLite(t)
	store, embedder := twoSourceCorpus(t)
	_ = embedder

	deleted, err := store.DeleteBySource("project-alpha", "records-process request", "release-owner")
	if err != nil {
		t.Fatalf("DeleteBySource: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}

	var reason, policyType, status, authorizedBy, source string
	var targetCount, deletedCount int
	err = store.db.QueryRow(`
		SELECT reason, policy_type, status, authorized_by, source, target_count, deleted_count
		FROM deletion_runs
	`).Scan(&reason, &policyType, &status, &authorizedBy, &source, &targetCount, &deletedCount)
	if err != nil {
		t.Fatalf("reading deletion evidence: %v", err)
	}
	if status != "complete" {
		t.Errorf("status = %q; only a completed run may be read as proof of removal", status)
	}
	if reason != "records-process request" || authorizedBy != "release-owner" {
		t.Errorf("evidence lost its attribution: reason=%q authorized_by=%q", reason, authorizedBy)
	}
	if source != "project-alpha" || policyType != "source" || targetCount != 1 || deletedCount != 1 {
		t.Errorf("evidence = source %q policy %q target %d deleted %d",
			source, policyType, targetCount, deletedCount)
	}

	// The content is gone; the evidence remains. The other source is untouched.
	var alpha, beta int
	if err := store.db.QueryRow(
		`SELECT COUNT(*) FROM messages WHERE source = 'project-alpha'`).Scan(&alpha); err != nil {
		t.Fatalf("counting alpha: %v", err)
	}
	if err := store.db.QueryRow(
		`SELECT COUNT(*) FROM messages WHERE source = 'project-beta'`).Scan(&beta); err != nil {
		t.Fatalf("counting beta: %v", err)
	}
	if alpha != 0 {
		t.Errorf("deletion left %d message(s) behind", alpha)
	}
	if beta != 1 {
		t.Errorf("a source-scoped deletion reached another source (beta count %d)", beta)
	}
}

// TestDeletionEvidenceCarriesNoContent: the evidence table is digests and
// counts, never bodies. It outlives the content, so anything it carries is
// content that was never actually deleted.
func TestDeletionEvidenceCarriesNoContent(t *testing.T) {
	requireSQLite(t)
	store := setupTestDB(t)
	defer func() { _ = store.Close() }()
	embedder := NewLocalHashingEmbedder(128)

	const secretish = "correlation-id-9e13b7f4-do-not-retain"
	seedScopedCorpus(t, store, embedder, []struct {
		source         string
		classification string
		body           string
	}{
		{"project-alpha", "internal", secretish},
	})

	if _, err := store.DeleteBySource("project-alpha", "records-process request", "release-owner"); err != nil {
		t.Fatalf("DeleteBySource: %v", err)
	}

	rows, err := store.db.Query(`SELECT * FROM deletion_runs`)
	if err != nil {
		t.Fatalf("reading deletion evidence: %v", err)
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("Columns: %v", err)
	}
	for rows.Next() {
		cells := make([]any, len(cols))
		holders := make([]sqlNullish, len(cols))
		for i := range cells {
			cells[i] = &holders[i]
		}
		if err := rows.Scan(cells...); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		for i, h := range holders {
			if strings.Contains(h.String(), secretish) {
				t.Errorf("deletion evidence column %q carries deleted content", cols[i])
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
}

// TestARefusedDeletionWritesNoEvidence: a refusal must write nothing, so no
// evidence row can imply a deletion that never ran.
func TestARefusedDeletionWritesNoEvidence(t *testing.T) {
	requireSQLite(t)
	store, _ := twoSourceCorpus(t)

	if _, err := store.DeleteBySource("", "no source named", "release-owner"); err == nil {
		t.Fatal("expected a refusal for an unnamed source")
	}
	if _, err := store.DeleteByClassification("", "no classification named", "release-owner"); err == nil {
		t.Fatal("expected a refusal for an unnamed classification")
	}
	if _, err := store.DeleteByAge(-1, nil, "negative age", "release-owner"); err == nil {
		t.Fatal("expected a refusal for a negative age")
	}

	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM deletion_runs`).Scan(&count); err != nil {
		t.Fatalf("counting deletion evidence: %v", err)
	}
	if count != 0 {
		t.Errorf("refused deletions wrote %d evidence row(s)", count)
	}

	var messages int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&messages); err != nil {
		t.Fatalf("counting messages: %v", err)
	}
	if messages != 2 {
		t.Errorf("a refused deletion removed content: %d messages remain, want 2", messages)
	}
}

// sqlNullish scans any SQLite column into a comparable string without
// caring about its declared type.
type sqlNullish struct{ raw any }

func (n *sqlNullish) Scan(value any) error {
	n.raw = value
	return nil
}

func (n *sqlNullish) String() string {
	switch v := n.raw.(type) {
	case nil:
		return ""
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return ""
	}
}
