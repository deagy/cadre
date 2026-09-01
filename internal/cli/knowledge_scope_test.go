package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deagy/recall/core"
	recallstore "github.com/deagy/recall/store"

	"github.com/deagy/cadre/cli/internal/knowledge"
	"github.com/deagy/cadre/cli/internal/retrieval"
)

// Command-line enforcement of knowledge-store scope, classification and the
// untrusted-data envelope.
//
// These exist because the library layer was not where the guarantees were
// being bypassed: the CLI computed `AllSources: *sources == ""`, so omitting
// --source was translated into an explicit "span every project in the store"
// and satisfied the library's gate on the way past. A guard that every real
// caller routes around is not a guard, so each test below goes through the
// same handler the dispatcher calls.
//
// The store underneath is now recall's, reached through recall/govern. The
// tests did not change shape when it moved, which is the point of having had
// them.
//
// As before: source, classification, agent and task are caller-asserted and
// authenticated by nobody. These assert that filters are applied honestly to
// an honest caller, never that a dishonest one is stopped.

// captureStdout runs fn with os.Stdout redirected to a temp file and returns
// what it wrote.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	previous := os.Stdout
	os.Stdout = file
	defer func() {
		os.Stdout = previous
		_ = file.Close()
	}()
	fn()
	if err := file.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	data, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return string(data)
}

// scopedCorpus seeds two projects' content into one recall store, the shape
// the shared global-fallback tier actually has, and returns the resolved env
// plus the directory holding the store and its audit log.
func scopedCorpus(t *testing.T) (knowledgeEnv, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "store.db")

	provider := knowledge.NewLocalHashingEmbedder(128)
	store, err := recallstore.NewSQLiteStore(recallstore.Config{
		Namespace: "default",
		Embedder:  retrieval.NewProviderAdapter(provider, 128),
	}, dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}

	// Bodies are over recall's default MinChunkSize of 50 runes. A shorter
	// one is dropped by the chunker without an error, where cadre's engine
	// stored every message it was given -- a behavioural difference the
	// migration inherits and this seed would otherwise hide.
	rows := []struct{ source, body string }{
		{"project-alpha", "alpha deployment runbook: how the alpha service is released and rolled back"},
		{"project-beta", "beta deployment runbook: how the beta service is released and rolled back"},
	}
	for _, row := range rows {
		doc := core.NewDocument("doc-"+row.source, row.source+" runbook", row.source)
		doc.Metadata[retrieval.MetaSource] = core.String{Value: row.source}
		doc.Metadata[retrieval.MetaClassification] = core.String{Value: "internal"}
		doc.Metadata[retrieval.MetaConversationID] = core.String{Value: "conv-" + row.source}
		doc.Metadata[retrieval.MetaMessageID] = core.String{Value: "msg-" + row.source}
		doc.Metadata[retrieval.MetaContentHash] = core.String{Value: "hash-" + row.source}
		doc.Metadata[retrieval.MetaRole] = core.String{Value: "user"}
		// A stored URI the bundle must never return.
		doc.Metadata["source_uri"] = core.String{Value: "/home/someone/private/" + row.source + ".json"}
		if err := store.Upload(context.Background(), doc, row.body); err != nil {
			t.Fatalf("Upload: %v", err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// The seed embedded with cadre's own provider, so record that as the
	// store's identity -- the same statement `cadre knowledge init` makes.
	if err := retrieval.WriteIdentity(dbPath, retrieval.StoreIdentity{
		Embedder: "local-hashing", Model: "hashing-128d", Dimensions: 128,
	}); err != nil {
		t.Fatalf("WriteIdentity: %v", err)
	}
	return testEnv(t, dbPath), dir
}

// TestASearchRefusesAStoreWithNoRecordedEmbedder: recall records nothing
// about what embedded a store, and a store queried by a different embedder
// does not fail -- it returns every chunk in scope at score 0, which reads
// as a normal result set. The identity has to be stated before it can be
// checked.
func TestASearchRefusesAStoreWithNoRecordedEmbedder(t *testing.T) {
	env, dir := scopedCorpus(t)
	if err := os.Remove(retrieval.IdentityPath(filepath.Join(dir, "store.db"))); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	stderr := captureStderr(t, func() {
		if code := knowledgeSearch(env, []string{
			"--classification", "internal", "--source", "project-alpha", "runbook",
		}); code == 0 {
			t.Fatal("a store with no recorded embedder was searched")
		}
	})
	if !strings.Contains(stderr, "cadre knowledge init") {
		t.Errorf("the refusal does not say how to fix it: %s", stderr)
	}
}

// TestASearchRefusesAMismatchedEmbedder is the failure this guard exists for:
// the store was embedded at one width and the config would query it at
// another. Removing the check was measured: the search succeeds, returns
// every chunk in scope at score 0, and records the wrong embedder.
func TestASearchRefusesAMismatchedEmbedder(t *testing.T) {
	env, dir := scopedCorpus(t)
	dbPath := filepath.Join(dir, "store.db")
	if err := retrieval.WriteIdentity(dbPath, retrieval.StoreIdentity{
		Embedder: "local-hashing", Model: "hashing-384d", Dimensions: 384,
	}); err != nil {
		t.Fatalf("WriteIdentity: %v", err)
	}

	stderr := captureStderr(t, func() {
		if code := knowledgeSearch(env, []string{
			"--classification", "internal", "--source", "project-alpha", "runbook",
		}); code == 0 {
			t.Fatal("a mismatched embedder was accepted")
		}
	})
	if !strings.Contains(stderr, "384") || !strings.Contains(stderr, "128") {
		t.Errorf("the refusal does not name both identities: %s", stderr)
	}
	if rows := auditRows(t, dir); len(rows) != 0 {
		t.Errorf("a refused search wrote %d audit row(s)", len(rows))
	}
}

// auditRows reads the retrieval audit log, which is the only record a
// completed retrieval leaves.
func auditRows(t *testing.T, dir string) []retrieval.AuditRow {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "retrievals.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("reading audit log: %v", err)
	}
	var rows []retrieval.AuditRow
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var row retrieval.AuditRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("parsing audit row %q: %v", line, err)
		}
		rows = append(rows, row)
	}
	return rows
}

// TestSearchCLIRefusesAnUnscopedRead is the exploitable hole this file
// exists for. Omitting --source used to be converted into --all-sources, so
// a caller who believed they had said nothing about scope performed a
// cross-project read of a shared store.
func TestSearchCLIRefusesAnUnscopedRead(t *testing.T) {
	env, dir := scopedCorpus(t)

	output := captureStdout(t, func() {
		captureStderr(t, func() {
			if code := knowledgeSearch(env, []string{
				"--classification", "internal", "runbook",
			}); code != 2 {
				t.Errorf("exit code = %d, want 2 for an unscoped read", code)
			}
		})
	})
	if strings.Contains(output, "runbook") {
		t.Errorf("a refused read still emitted stored content: %q", output)
	}
	if rows := auditRows(t, dir); len(rows) != 0 {
		t.Errorf("refused reads wrote %d audit row(s)", len(rows))
	}
}

// TestSearchCLIRefusesAmbiguousScope: naming sources and asking for all of
// them is not a choice, and resolving it either way would be a guess.
func TestSearchCLIRefusesAmbiguousScope(t *testing.T) {
	env, _ := scopedCorpus(t)

	captureStderr(t, func() {
		code := knowledgeSearch(env, []string{
			"--classification", "internal",
			"--source", "project-alpha",
			"--all-sources",
			"runbook",
		})
		if code != 2 {
			t.Errorf("exit code = %d, want 2 when both --source and --all-sources are given", code)
		}
	})
}

// TestSearchCLIRefusesABlankSourceFilter closes the retype where a blank
// value is accepted and then silently drops the scope clause entirely.
func TestSearchCLIRefusesABlankSourceFilter(t *testing.T) {
	env, _ := scopedCorpus(t)

	for _, value := range []string{"", "   ", ","} {
		captureStderr(t, func() {
			code := knowledgeSearch(env, []string{
				"--classification", "internal", "--source", value, "runbook",
			})
			if code == 0 {
				t.Errorf("--source %q was accepted", value)
			}
		})
	}
}

// TestSearchCLIRefusesAnUnrecognisedClassification: content ingested or read
// under a label no policy describes is content nobody can handle correctly.
func TestSearchCLIRefusesAnUnrecognisedClassification(t *testing.T) {
	env, _ := scopedCorpus(t)

	for _, classification := range []string{"general", "restricted;", "%"} {
		captureStderr(t, func() {
			code := knowledgeSearch(env, []string{
				"--classification", classification, "--all-sources", "runbook",
			})
			if code != 2 {
				t.Errorf("--classification %q returned %d, want 2", classification, code)
			}
		})
	}
}

// TestAScopedSearchIsServedCitedAndRecorded is the whole path end to end:
// a request that states everything it must is served from recall, comes back
// inside the trust envelope with its citation, and leaves exactly one audit
// row carrying the embedder identity that produced the vectors.
func TestAScopedSearchIsServedCitedAndRecorded(t *testing.T) {
	env, dir := scopedCorpus(t)

	output := captureStdout(t, func() {
		if code := knowledgeSearch(env, []string{
			"--classification", "internal",
			"--source", "project-alpha",
			"--agent", "test-agent",
			"--task-id", "T-04",
			"--json",
			"runbook",
		}); code != 0 {
			t.Fatalf("a fully-specified search was refused: exit %d", code)
		}
	})

	var bundle retrieval.Bundle
	if err := json.Unmarshal([]byte(output), &bundle); err != nil {
		t.Fatalf("parsing bundle: %v\n%s", err, output)
	}
	if bundle.Trust != retrieval.TrustLabel {
		t.Errorf("trust label = %q, want %q", bundle.Trust, retrieval.TrustLabel)
	}
	if len(bundle.Requirements) == 0 {
		t.Error("bundle carries no handling requirements")
	}
	if bundle.Count == 0 {
		t.Fatal("a scoped search over seeded content returned nothing")
	}
	if bundle.Results[0].Citation.Source != "project-alpha" {
		t.Errorf("citation source = %q, want project-alpha", bundle.Results[0].Citation.Source)
	}
	if strings.Contains(output, "source_uri") || strings.Contains(output, "/home/someone/private") {
		t.Errorf("the bundle returned a stored source URI:\n%s", output)
	}

	rows := auditRows(t, dir)
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Agent != "test-agent" || row.TaskID != "T-04" {
		t.Errorf("audit row lost the caller: %+v", row)
	}
	if row.Embedder == "" || row.Model == "" {
		t.Errorf("audit row cannot attribute the vectors it searched: %+v", row)
	}
	if row.ResultCount != bundle.Count {
		t.Errorf("audit row counted %d results, bundle carried %d", row.ResultCount, bundle.Count)
	}
	if row.AllSources || len(row.SourceFilters) != 1 || row.SourceFilters[0] != "project-alpha" {
		t.Errorf("audit row lost the scope: %+v", row)
	}
}

// TestKnowledgeCmdFailsClosedOnAMissingConfig: --config names a config file.
// Read as a database path, a mistyped value produced a working, empty store
// that answered every query with zero results.
func TestKnowledgeCmdFailsClosedOnAMissingConfig(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "no-such-config.json")

	captureStderr(t, func() {
		if code := KnowledgeCmd([]string{"--config", missing, "config"}); code == 0 {
			t.Error("a missing --config was accepted")
		}
	})
	if _, err := os.Stat(missing); err == nil {
		t.Error("a refused invocation created the named path")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("a refused invocation created %d file(s) in the config directory", len(entries))
	}
}
