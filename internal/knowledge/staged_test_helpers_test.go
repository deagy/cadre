package knowledge

import (
	"path/filepath"
	"testing"
)

const (
	testStagedProposer = "proposing-agent"
	testStagedSteward  = "knowledge-store-steward"
	testStagedBody     = "The staged-records subsystem enforces authorship/approval separation.\n"
)

// testStagedFrontmatter builds a well-formed 'proposed' record. Every field is
// supplied explicitly, and the digest is computed through the one
// implementation, so a test can never accidentally assert against a
// hand-computed digest that drifted.
func testStagedFrontmatter(recordID string) map[string]any {
	return map[string]any{
		"id":     recordID,
		"title":  "An example finding",
		"status": "proposed",
		"evidence": []any{
			"internal/knowledge/staged_store.go:1",
		},
		"origin": map[string]any{
			"task":     "TASK-1",
			"artifact": "internal/knowledge/staged_store.go",
			"revision": "abc1234",
		},
		"proposed_classification":    "internal",
		"source_scope":               "cadre",
		"sensitivity_notes":          "",
		"conflicts_or_staleness":     "",
		"recommended_action":         "ingest",
		"untrusted_instruction_risk": false,
		"staged_by":                  testStagedProposer,
		"content_digest":             ComputeStagedDigest(testStagedBody),
	}
}

// testStagedStore opens a fresh store in a temp directory with the
// staged-record schema installed.
func testStagedStore(t *testing.T) *Store {
	t.Helper()
	requireSQLite(t)
	store, err := Open(filepath.Join(t.TempDir(), "knowledge.db"))
	if err != nil {
		t.Fatalf("cannot open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.InstallStagedSchema(); err != nil {
		t.Fatalf("cannot install staged schema: %v", err)
	}
	return store
}

// testStageRecord puts a well-formed proposed record and returns its id.
func testStageRecord(t *testing.T, store *Store, recordID string) string {
	t.Helper()
	stored, err := store.PutStagedRecord(testStagedFrontmatter(recordID), testStagedBody)
	if err != nil {
		t.Fatalf("cannot stage %s: %v", recordID, err)
	}
	return stored
}
