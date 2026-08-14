package contextstore

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Structural boundary between the two stores.
//
// This is the Go guard for the invariants
// roster/orchestration/test/test_context_boundary.py asserted on the Python
// side: the two stores cannot share a database, and neither imports the
// other. That Python test now fails at import time because the knowledge
// store's Python modules were deleted, and this package's own doc comment
// said the Go port had "no equivalent automated guard yet ... preserved here
// by convention: grep internal/contextstore for internal/knowledge before
// merging". Convention is not a guard. This file is.
//
// It also pins the third invariant -- the context store cannot embed
// remotely -- which matters more here than in the knowledge store: context
// entries are unreviewed agent working material, and whether it may leave
// the machine is an open security decision (OD-5), not a config toggle.

const (
	contextStorePkg   = "github.com/deagy/cadre/cli/internal/contextstore"
	knowledgeStorePkg = "github.com/deagy/cadre/cli/internal/knowledge"
)

// packageImports returns every import path appearing in the .go files of
// dir, mapped to the files that import it.
//
// It reads every .go file in the directory rather than going through the
// build system on purpose: test files count (a test-only import couples the
// two packages just as effectively, and sets the precedent), and so does a
// file excluded by a build tag -- a boundary that a tag can switch off is
// not a boundary.
func packageImports(t *testing.T, dir string) map[string][]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	found := map[string][]string{}
	inspected := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		inspected++
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("bad import literal in %s: %v", name, err)
			}
			found[path] = append(found[path], name)
		}
	}
	if inspected == 0 {
		t.Fatalf("no .go files found in %s; this boundary check would pass vacuously", dir)
	}
	return found
}

// TestNeitherStoreImportsTheOther. The context store holds unreviewed agent
// scratch; the knowledge store holds steward-dispositioned, retrievable
// corpus. An import in either direction is how the two trust levels get
// quietly merged.
func TestNeitherStoreImportsTheOther(t *testing.T) {
	contextImports := packageImports(t, ".")
	if files, ok := contextImports[knowledgeStorePkg]; ok {
		t.Errorf("internal/contextstore imports internal/knowledge in %v; "+
			"agent scratch and the curated corpus must not share code", files)
	}

	knowledgeDir := filepath.Join("..", "knowledge")
	if _, err := os.Stat(knowledgeDir); err != nil {
		t.Fatalf("internal/knowledge is missing; this boundary test has nothing to check: %v", err)
	}
	knowledgeImports := packageImports(t, knowledgeDir)
	if files, ok := knowledgeImports[contextStorePkg]; ok {
		t.Errorf("internal/knowledge imports internal/contextstore in %v; "+
			"the curated corpus must not reach into unreviewed agent scratch", files)
	}
}

// TestTheContextStoreCannotEmbedRemotely. Extending the provider list is not
// a configuration change; it is a decision about whether unreviewed working
// material may be transmitted to a third-party endpoint.
func TestTheContextStoreCannotEmbedRemotely(t *testing.T) {
	if len(SupportedEmbeddingProviders) != 1 || SupportedEmbeddingProviders[0] != "hashing" {
		t.Fatalf("SupportedEmbeddingProviders = %v, want exactly [hashing]; widening this "+
			"list is a security decision (OD-5), not a config change", SupportedEmbeddingProviders)
	}

	dir := t.TempDir()
	for _, provider := range []string{
		"openai-compatible", "openai", "remote", "http", "hashing-remote", "Hashing", "",
	} {
		path := filepath.Join(dir, "config.json")
		writeConfig(t, path, `{"embedding": {"provider": "`+provider+`", "model": "m", "dimensions": 384}}`)
		if _, _, err := LoadConfig(path); err == nil {
			t.Errorf("LoadConfig accepted embedding provider %q", provider)
		}
	}

	// The refusal is structural, not just a list check: the module that
	// would perform a remote embedding must not be reachable from here at
	// all. No file in this package may open a network connection.
	imports := packageImports(t, ".")
	for _, banned := range []string{"net/http", "net", "net/url", knowledgeStorePkg} {
		if files, ok := imports[banned]; ok {
			t.Errorf("internal/contextstore imports %q in %v; a store that refuses remote "+
				"embedding should have no way to reach the network", banned, files)
		}
	}
}

// TestTheTwoStoresCannotShareADatabase. They resolve their config, and
// therefore their database, from different locations at every tier. Sharing
// one file would put steward-dispositioned corpus and unreviewed scratch
// behind one set of filters.
func TestTheTwoStoresCannotShareADatabase(t *testing.T) {
	// Project-local tier: different relative config paths.
	knowledgeRelative := filepath.Join(".agents", "knowledge-store", "config.json")
	if ProjectLocalRelativePath == knowledgeRelative {
		t.Fatalf("both stores resolve their project-local config from %s", ProjectLocalRelativePath)
	}
	if !strings.Contains(ProjectLocalRelativePath, "context-store") {
		t.Errorf("ProjectLocalRelativePath = %q, expected it under .agents/context-store",
			ProjectLocalRelativePath)
	}

	// Explicit-config tier: the resolved database is anchored to the config
	// directory, so two configs in different directories cannot collide by
	// both saying "./data/store.db".
	dirA := t.TempDir()
	dirB := t.TempDir()
	pathA := filepath.Join(dirA, "config.json")
	pathB := filepath.Join(dirB, "config.json")
	writeConfig(t, pathA, `{"database": "./store.db"}`)
	writeConfig(t, pathB, `{"database": "./store.db"}`)

	cfgA, tierA, err := LoadConfig(pathA)
	if err != nil {
		t.Fatalf("LoadConfig(A): %v", err)
	}
	cfgB, _, err := LoadConfig(pathB)
	if err != nil {
		t.Fatalf("LoadConfig(B): %v", err)
	}
	if tierA != TierExplicitConfig {
		t.Errorf("tier = %q, want %q", tierA, TierExplicitConfig)
	}
	if cfgA.Database == cfgB.Database {
		t.Errorf("two configs in different directories resolved to the same database: %s",
			cfgA.Database)
	}
	if !strings.HasPrefix(cfgA.Database, dirA) {
		t.Errorf("database %q is not anchored to its config directory %q", cfgA.Database, dirA)
	}
}

// TestLoadConfigFailsClosedOnAMissingExplicitPath. An explicitly named
// config that does not exist must refuse, never silently fall back to the
// shared global default -- that fallback is the difference between reading
// your own partition and reading everyone's.
func TestLoadConfigFailsClosedOnAMissingExplicitPath(t *testing.T) {
	dir := t.TempDir()
	cases := []string{
		filepath.Join(dir, "does-not-exist.json"),
		filepath.Join(dir, "nested", "missing", "config.json"),
		dir, // a directory is not a config file
	}
	for _, path := range cases {
		cfg, tier, err := LoadConfig(path)
		if err == nil {
			t.Errorf("LoadConfig(%q) succeeded (tier %q, db %q); a named-but-absent config "+
				"must fail closed", path, tier, cfg.Database)
		}
	}
}

// TestAnExpiredEntryIsUnreadableAndIndistinguishableFromAnAbsentOne.
// Every context entry expires -- there is no indefinite entry. An expired
// one must read exactly like a handle that never existed, so a caller
// cannot probe for entries it may not read.
func TestAnExpiredEntryIsUnreadableAndIndistinguishableFromAnAbsentOne(t *testing.T) {
	requireSQLite(t)
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "context.db")

	db, err := OpenStore(dbPath, true)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	cfg := testConfig()
	put, err := PutEntry(db, cfg, PutOptions{
		Scope: "agent", Classification: "internal", Agent: "test-engineer", TaskID: "TASK-1",
		Label: "expiring entry", Source: "demo", Content: "scratch that should not outlive its ttl",
	})
	if err != nil {
		t.Fatalf("PutEntry: %v", err)
	}

	caller := CallerOptions{Agent: "test-engineer", TaskID: "TASK-1", Classification: "internal", Source: "demo"}

	// Readable while live.
	bundle, err := GetEntry(db, GetOptions{Handle: put.Handle, CallerOptions: caller})
	if err != nil {
		t.Fatalf("GetEntry (live): %v", err)
	}
	if len(bundle.Results) != 1 {
		t.Fatalf("a live entry should be readable, got %d results", len(bundle.Results))
	}

	// Force it into the past, then reopen: expiry is enforced by the
	// open-time sweep, so this is the state a later invocation sees.
	if _, err := db.Exec(
		`UPDATE entries SET expires_at = ? WHERE handle = ?`,
		"2000-01-01T00:00:00.000Z", put.Handle); err != nil {
		t.Fatalf("ageing the entry: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db, err = OpenStore(dbPath, true)
	if err != nil {
		t.Fatalf("OpenStore (reopen): %v", err)
	}
	defer func() { _ = db.Close() }()

	expiredBundle, err := GetEntry(db, GetOptions{Handle: put.Handle, CallerOptions: caller})
	if err != nil {
		t.Fatalf("GetEntry (expired): %v", err)
	}
	if len(expiredBundle.Results) != 0 {
		t.Errorf("an expired entry was still readable (%d results)", len(expiredBundle.Results))
	}

	// An absent handle produces the same shape: no error, no results.
	absent := "ctx_" + strings.Repeat("a", 32)
	if absent == put.Handle {
		t.Fatalf("the synthetic absent handle collided with the real one")
	}
	absentBundle, err := GetEntry(db, GetOptions{Handle: absent, CallerOptions: caller})
	if err != nil {
		t.Fatalf("GetEntry (absent handle): %v", err)
	}
	if len(absentBundle.Results) != len(expiredBundle.Results) {
		t.Errorf("an expired handle (%d results) is distinguishable from an absent one (%d)",
			len(expiredBundle.Results), len(absentBundle.Results))
	}
}

func writeConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func testConfig() *Config {
	return &Config{
		Ingestion: map[string]any{"redact_secrets": true},
		Expiry: Expiry{
			DefaultTTLDaysByScope: map[string]int{"agent": 1, "dispatch": 7, "project": 30},
			MaximumTTLDays:        90,
		},
		Limits:    map[string]any{"max_entry_bytes": 1048576},
		Chunking:  Chunking{MaxCharacters: 2400, OverlapCharacters: 240},
		Embedding: Embedding{Provider: "hashing", Model: "feature-hash-v1", Dimensions: 384},
	}
}
