package retrieval

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deagy/recall/core"
	"github.com/deagy/recall/govern"
	"github.com/deagy/recall/index"
)

// TestEmbedderIdentityRefusesWhatCannotBeAttributed.
//
// The engine refused a search with no embedding provider. recall takes its
// embedder at construction, so the refusal moved here -- but its reason did
// not change: the provider and model are written into every audit row, and a
// retrieval recorded against an unnamed model cannot be reproduced against
// the vectors it searched.
func TestEmbedderIdentityRefusesWhatCannotBeAttributed(t *testing.T) {
	cases := []struct {
		name       string
		provider   string
		model      string
		dimensions int
		wantErr    string
	}{
		{"no provider", "", "", 128, "embedding provider is required"},
		{"remote provider with no model", "openai-compatible", "", 128, "embedding model is required"},
		{"local hashing with no width", "local-hashing", "", 0, "positive"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := EmbedderIdentity(c.provider, c.model, c.dimensions)
			if err == nil {
				t.Fatalf("%s was accepted", c.name)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, c.wantErr)
			}
		})
	}
}

// TestLocalHashingIdentityCarriesItsWidth: the hashing embedder has no model
// name, but its output width changes the vectors it produces, so the width is
// the identity. Two stores embedded at different widths must not look alike
// in the audit log.
func TestLocalHashingIdentityCarriesItsWidth(t *testing.T) {
	_, narrow, err := EmbedderIdentity("local-hashing", "", 128)
	if err != nil {
		t.Fatalf("EmbedderIdentity: %v", err)
	}
	_, wide, err := EmbedderIdentity("local-hashing", "", 384)
	if err != nil {
		t.Fatalf("EmbedderIdentity: %v", err)
	}
	if narrow == wide {
		t.Errorf("both widths record the same identity %q", narrow)
	}
}

// TestTheAuditLogIsSyncedBeforeTheRetrievalReturns: a row still in the page
// cache when the process dies is a retrieval that happened and was never
// recorded. The observable half of that guarantee is that the row is readable
// the instant RecordRetrieval returns.
func TestTheAuditLogIsSyncedBeforeTheRetrievalReturns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "retrievals.jsonl")
	log, err := NewAuditLog(path)
	if err != nil {
		t.Fatalf("NewAuditLog: %v", err)
	}

	entry := govern.Entry{
		Query: "a question", Classification: "internal",
		SourceFilters: []string{"project-alpha"}, Agent: "agent", TaskID: "T-04",
		ResultCount: 3, Embedder: "local-hashing", Model: "hashing-128d",
	}
	if err := log.RecordRetrieval(context.Background(), entry); err != nil {
		t.Fatalf("RecordRetrieval: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	var row AuditRow
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &row); err != nil {
		t.Fatalf("parsing the row: %v", err)
	}
	if row.ResultCount != 3 || row.Agent != "agent" || row.TaskID != "T-04" {
		t.Errorf("row lost the retrieval: %+v", row)
	}
	if row.Embedder == "" || row.Model == "" {
		t.Errorf("row cannot attribute the vectors searched: %+v", row)
	}
}

// TestTheAuditLogRecordsTheQueryIDNotTheQuery: the log correlates with the
// bundle it produced without becoming a second copy of what people searched
// for.
func TestTheAuditLogRecordsTheQueryIDNotTheQuery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "retrievals.jsonl")
	log, err := NewAuditLog(path)
	if err != nil {
		t.Fatalf("NewAuditLog: %v", err)
	}
	const query = "a distinctive phrase nobody else would write"
	if err := log.RecordRetrieval(context.Background(), govern.Entry{
		Query: query, Classification: "internal", AllSources: true,
		Embedder: "local-hashing", Model: "hashing-128d",
	}); err != nil {
		t.Fatalf("RecordRetrieval: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the log: %v", err)
	}
	if strings.Contains(string(data), query) {
		t.Errorf("the audit log stored the query text:\n%s", data)
	}
	if !strings.Contains(string(data), StableQueryID(query)) {
		t.Errorf("the audit log cannot be correlated with its bundle:\n%s", data)
	}
}

// TestARetrievalThatCannotBeRecordedIsRefused is the falsification of the
// audit guarantee: govern fails a retrieval whose recording fails, and this
// proves the wiring actually surfaces that rather than serving results the
// system cannot account for.
func TestARetrievalThatCannotBeRecordedIsRefused(t *testing.T) {
	dir := t.TempDir()
	// A directory where the log file must go: opening it for append fails.
	logPath := filepath.Join(dir, "retrievals.jsonl")
	if err := os.MkdirAll(logPath, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	log, err := NewAuditLog(logPath)
	if err != nil {
		t.Fatalf("NewAuditLog: %v", err)
	}

	store, err := govern.New(servingSearcher{}, log, "local-hashing", "hashing-128d")
	if err != nil {
		t.Fatalf("govern.New: %v", err)
	}

	results, err := store.Search(context.Background(), govern.Request{
		Query: "anything", Classification: "internal", AllSources: true,
	})
	if err == nil {
		t.Fatalf("a retrieval that could not be recorded returned %d results", len(results))
	}
	if !strings.Contains(err.Error(), "could not be recorded") {
		t.Errorf("error = %q, want it to name the recording failure", err)
	}
}

// servingSearcher always returns a result, so the only thing that can fail
// the retrieval above is the recording.
type servingSearcher struct{}

func (servingSearcher) Search(context.Context, string, index.SearchOptions) ([]index.SearchResult, error) {
	return []index.SearchResult{{
		Chunk: &core.Chunk{ID: "chunk-1", Content: "content"},
		Score: 1,
	}}, nil
}

// TestTheBundleNeverCarriesAStoredURI. SECURITY.md drops source_uri because a
// stored URI can expose a local filesystem path from the machine that
// performed the ingestion. The chunk metadata carries one here; the bundle
// must not.
func TestTheBundleNeverCarriesAStoredURI(t *testing.T) {
	results := ResultsFrom([]index.SearchResult{{
		Chunk: &core.Chunk{
			ID:      "chunk-1",
			Content: "stored content",
			Metadata: map[string]core.Value{
				MetaSource:         core.String{Value: "project-alpha"},
				MetaClassification: core.String{Value: "internal"},
				"source_uri":       core.String{Value: "/home/someone/private/export.json"},
			},
		},
		Score: 0.9,
	}})

	bundle := NewBundle(BundleScope{
		Query: "q", Classification: "internal", SourceFilters: []string{"project-alpha"},
	}, "vector", results)

	encoded, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshalling the bundle: %v", err)
	}
	if strings.Contains(string(encoded), "/home/someone/private") {
		t.Errorf("the bundle leaked a stored URI:\n%s", encoded)
	}
	if bundle.Trust != TrustLabel {
		t.Errorf("trust = %q, want %q", bundle.Trust, TrustLabel)
	}
	if len(bundle.Requirements) == 0 {
		t.Error("the bundle carries no handling requirements")
	}
}

// TestAWideReadIsDistinguishableFromAScopedOne: SourceFilter is nil for an
// all-sources read and AllSources is true, so a reader can tell a deliberately
// wide read from a scoped one without inferring it from an empty list.
func TestAWideReadIsDistinguishableFromAScopedOne(t *testing.T) {
	wide := NewBundle(BundleScope{Query: "q", Classification: "internal", AllSources: true}, "vector", nil)
	if !wide.AllSources || wide.SourceFilter != nil {
		t.Errorf("a wide read reads as scoped: all_sources=%v filter=%v", wide.AllSources, wide.SourceFilter)
	}

	scoped := NewBundle(BundleScope{
		Query: "q", Classification: "internal", SourceFilters: []string{"project-alpha"},
	}, "vector", nil)
	if scoped.AllSources || len(scoped.SourceFilter) != 1 {
		t.Errorf("a scoped read reads as wide: all_sources=%v filter=%v",
			scoped.AllSources, scoped.SourceFilter)
	}
}

// TestOpenRefusesBeforeCreatingAnything: Open resolves the embedder identity
// before it opens the store, so a wiring that could never record a
// reproducible retrieval does not leave a database behind.
func TestOpenRefusesBeforeCreatingAnything(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "store.db")

	_, err := Open(Options{Database: dbPath, EmbedderName: "openai-compatible", Dimensions: 128},
		stubProvider{})
	if err == nil {
		t.Fatal("a store with no model identity was opened")
	}
	if _, statErr := os.Stat(dbPath); statErr == nil {
		t.Errorf("%s was created by a refused Open", dbPath)
	}
}

type stubProvider struct{}

func (stubProvider) Embed(texts []string) ([][]float64, error) {
	out := make([][]float64, len(texts))
	for i := range texts {
		out[i] = make([]float64, 128)
	}
	return out, nil
}
