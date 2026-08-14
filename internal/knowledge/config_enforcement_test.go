package knowledge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Configuration resolution, classification validation and retention refusal.
//
// These are the negative cases: what LoadConfig refuses, what it will not
// silently invent, and which classification/retention combinations cannot
// reach the store. The positive cases are here only as controls, so a
// refusal cannot pass by virtue of everything being broken.
//
// None of these need SQLite -- nothing here opens a database, which is
// itself part of the contract: config resolution must fail before anything
// is created on disk.

// writeConfigFile writes a config.json and returns its path.
func writeConfigFile(t *testing.T, dir string, raw map[string]any) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// chdir moves into dir for the duration of the test. Project-local
// resolution walks up from the working directory, so a test that exercises
// it has to have one.
func chdir(t *testing.T, dir string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
}

// isolatedProject makes dir look like a project root (a .git marker stops
// the upward walk) and moves into it, so no config above the temp directory
// can be picked up by accident.
func isolatedProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: elsewhere\n"), 0o600); err != nil {
		t.Fatalf("writing .git marker: %v", err)
	}
	// Keep the global tier inside the temp tree too, so a test that falls
	// through to it never reads or writes the developer's real store.
	t.Setenv("KNOWLEDGE_STORE_HOME", filepath.Join(dir, "global-store"))
	chdir(t, dir)
	return dir
}

// --- Fail closed on a named-but-missing config -----------------------------

// TestLoadConfigRefusesAMissingExplicitConfig is the load-bearing one. The Go
// CLI read --config as a *database* path and handed it to knowledge.Open,
// which creates the directory and an empty schema -- so a mistyped path
// produced a working, empty, wrong store that answered every query with zero
// results instead of an error.
func TestLoadConfigRefusesAMissingExplicitConfig(t *testing.T) {
	dir := isolatedProject(t)
	missing := filepath.Join(dir, "nowhere", "config.json")

	cfg, tier, err := LoadConfig(missing)
	if err == nil {
		t.Fatalf("expected a refusal for a missing explicit config, got tier %q database %q",
			tier, cfg.Database)
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error should name the missing file, got: %v", err)
	}
	if cfg != nil {
		t.Error("a refused config load must return no config")
	}
	// And it must not have created anything on the way to refusing.
	if _, err := os.Stat(filepath.Join(dir, "nowhere")); !os.IsNotExist(err) {
		t.Error("a refused config load created a directory")
	}
}

// TestLoadConfigRefusesADirectoryAsConfig: a path that exists but is not a
// file is not a config, and must not be read as an empty one.
func TestLoadConfigRefusesADirectoryAsConfig(t *testing.T) {
	dir := isolatedProject(t)
	if _, _, err := LoadConfig(dir); err == nil {
		t.Fatal("expected a refusal when --config names a directory")
	}
}

// TestLoadConfigRefusesUnparseableConfig: a database file passed where a
// config file belongs (the old spelling of --config) must fail loudly, not
// resolve to defaults.
func TestLoadConfigRefusesUnparseableConfig(t *testing.T) {
	dir := isolatedProject(t)
	notJSON := filepath.Join(dir, "store.db")
	if err := os.WriteFile(notJSON, []byte("SQLite format 3\x00"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, _, err := LoadConfig(notJSON); err == nil {
		t.Fatal("expected a refusal for a config file that is not JSON")
	}
}

// TestLoadConfigRefusesANonObjectRoot pins the shape check.
func TestLoadConfigRefusesANonObjectRoot(t *testing.T) {
	dir := isolatedProject(t)
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`["database"]`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, _, err := LoadConfig(path); err == nil {
		t.Fatal("expected a refusal for a non-object configuration root")
	}
}

// --- Which tier answered ----------------------------------------------------

// TestLoadConfigTiers walks all three resolution tiers, because which one
// answered is the fact every scope decision downstream is allowed to depend
// on.
func TestLoadConfigTiers(t *testing.T) {
	dir := isolatedProject(t)

	// Global fallback: no project-local config exists yet.
	cfg, tier, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig (global): %v", err)
	}
	if tier != TierGlobalFallback {
		t.Errorf("tier = %q, want %q", tier, TierGlobalFallback)
	}
	globalDir := filepath.Join(dir, "global-store")
	if !strings.HasPrefix(cfg.Database, globalDir) {
		t.Errorf("global-tier database %q is not under KNOWLEDGE_STORE_HOME %q",
			cfg.Database, globalDir)
	}

	// Project-local: declaring a partition is what claims one.
	projectConfig := writeConfigFile(t, filepath.Join(dir, ".agents", "knowledge-store"), map[string]any{})
	cfg, tier, err = LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig (project-local): %v", err)
	}
	if tier != TierProjectLocal {
		t.Errorf("tier = %q, want %q", tier, TierProjectLocal)
	}
	want := filepath.Join(filepath.Dir(projectConfig), "store.db")
	if cfg.Database != want {
		t.Errorf("project-local database = %q, want %q", cfg.Database, want)
	}

	// Explicit config outranks both.
	explicit := writeConfigFile(t, filepath.Join(dir, "elsewhere"),
		map[string]any{"database": filepath.Join(dir, "elsewhere", "explicit.db")})
	_, tier, err = LoadConfig(explicit)
	if err != nil {
		t.Fatalf("LoadConfig (explicit): %v", err)
	}
	if tier != TierExplicitConfig {
		t.Errorf("tier = %q, want %q", tier, TierExplicitConfig)
	}
}

// TestProjectLocalDatabaseCannotEscapeItsConfigDirectory: a project-local
// config arrives with `git clone` and is editable by anyone who can open a
// pull request. It may choose where within its own partition the database
// lives; it may not point at somebody else's.
func TestProjectLocalDatabaseCannotEscapeItsConfigDirectory(t *testing.T) {
	dir := isolatedProject(t)
	writeConfigFile(t, filepath.Join(dir, ".agents", "knowledge-store"),
		map[string]any{"database": "../../../elsewhere/other-project.db"})

	if _, _, err := LoadConfig(""); err == nil {
		t.Fatal("expected a refusal for a project-local database outside its config directory")
	}

	// Control: a database inside the config directory is accepted, so the
	// refusal above is the containment check and not a broken loader.
	writeConfigFile(t, filepath.Join(dir, ".agents", "knowledge-store"),
		map[string]any{"database": "./nested/store.db"})
	if _, _, err := LoadConfig(""); err != nil {
		t.Fatalf("a contained project-local database was refused: %v", err)
	}
}

// TestProjectLocalConfigCannotEnableRemoteEmbeddings: whether this project's
// content is transmitted to a third-party endpoint is not a decision a
// cloned file gets to make.
func TestProjectLocalConfigCannotEnableRemoteEmbeddings(t *testing.T) {
	dir := isolatedProject(t)
	writeConfigFile(t, filepath.Join(dir, ".agents", "knowledge-store"), map[string]any{
		"embedding": map[string]any{"provider": "openai-compatible"},
	})

	_, _, err := LoadConfig("")
	if err == nil {
		t.Fatal("expected a refusal for remote embeddings in a project-local config")
	}
	if !strings.Contains(err.Error(), "remote embeddings") {
		t.Errorf("error should name the refused capability, got: %v", err)
	}
}

// TestLegacyHashingProviderStillLoads: the Python implementation this package
// replaced accepted {"hashing", "openai-compatible"}. The port renamed the
// first to "local-hashing" with no migration, so every config written before
// it -- including the default the Python implementation itself wrote to
// ~/.agents/knowledge-store/config.json -- stopped loading, taking every
// `cadre knowledge` command with it.
func TestLegacyHashingProviderStillLoads(t *testing.T) {
	dir := isolatedProject(t)
	path := writeConfigFile(t, filepath.Join(dir, "explicit"), map[string]any{
		"embedding": map[string]any{"provider": LegacyLocalEmbeddingProvider},
	})

	config, _, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("a config naming the legacy provider must still load: %v", err)
	}
	if config.Embedding.Provider != "local-hashing" {
		t.Errorf("Provider = %q, want it normalised to %q",
			config.Embedding.Provider, "local-hashing")
	}
}

// TestLegacyHashingProviderSatisfiesTheProjectLocalTrustGuard is the half
// that is easy to get wrong. The implicit-project-config guard compares the
// provider against "local-hashing" exactly. Had the legacy name merely been
// added to SupportedEmbeddingProviders instead of normalised before that
// comparison, a project-local config naming "hashing" would have been
// refused *as though it had asked for remote embeddings* -- the offline
// provider, rejected with a message about transmitting content off the
// machine.
func TestLegacyHashingProviderSatisfiesTheProjectLocalTrustGuard(t *testing.T) {
	dir := isolatedProject(t)
	writeConfigFile(t, filepath.Join(dir, ".agents", "knowledge-store"), map[string]any{
		"embedding": map[string]any{"provider": LegacyLocalEmbeddingProvider},
	})

	config, _, err := LoadConfig("")
	if err != nil {
		t.Fatalf("the legacy offline provider must pass the project-local guard: %v", err)
	}
	if config.Embedding.Provider != "local-hashing" {
		t.Errorf("Provider = %q, want %q", config.Embedding.Provider, "local-hashing")
	}
}

// TestLoadConfigRefusesAnUnknownEmbeddingProvider closes the obvious retype.
func TestLoadConfigRefusesAnUnknownEmbeddingProvider(t *testing.T) {
	dir := isolatedProject(t)
	path := writeConfigFile(t, filepath.Join(dir, "explicit"), map[string]any{
		"embedding": map[string]any{"provider": "whatever-is-handy"},
	})
	if _, _, err := LoadConfig(path); err == nil {
		t.Fatal("expected a refusal for an unsupported embedding provider")
	}
}

// --- Classification ---------------------------------------------------------

// TestValidateClassificationRefusesAnythingOutsideTheFourLabels.
//
// "general" is called out by name because it was this CLI's ingest default:
// content stored under it is content no retrieval policy describes, and
// nothing about the label tells a reviewer how it must be handled.
func TestValidateClassificationRefusesAnythingOutsideTheFourLabels(t *testing.T) {
	refused := []string{
		"general", "", "secret", "technical", "temporary",
		"INTERNAL", "Internal", "internal ", " internal", "internal;", "%",
	}
	for _, value := range refused {
		if _, err := ValidateClassification(value); err == nil {
			t.Errorf("classification %q was accepted", value)
		}
	}
	for _, value := range Classifications {
		if _, err := ValidateClassification(value); err != nil {
			t.Errorf("classification %q was refused: %v", value, err)
		}
	}
}

// TestLoadConfigRefusesAnUnrecognisedDefaultClassification: a config that
// sets an unrecognised ingestion default fails at load, not at whichever
// ingest first relies on it.
func TestLoadConfigRefusesAnUnrecognisedDefaultClassification(t *testing.T) {
	dir := isolatedProject(t)
	path := writeConfigFile(t, filepath.Join(dir, "explicit"), map[string]any{
		"ingestion": map[string]any{"default_classification": "general", "redact_secrets": true},
	})
	if _, _, err := LoadConfig(path); err == nil {
		t.Fatal("expected a refusal for an unrecognised ingestion.default_classification")
	}
}

// --- Retention --------------------------------------------------------------

// TestLoadConfigRefusesAConfiguredRestrictedRetentionDefault.
//
// ResolveRetentionUntil never reads a restricted entry from that map at all,
// so a config that sets one would be dead configuration that looks
// load-bearing -- the reader would believe restricted content ages out on a
// schedule nothing implements.
func TestLoadConfigRefusesAConfiguredRestrictedRetentionDefault(t *testing.T) {
	dir := isolatedProject(t)
	path := writeConfigFile(t, filepath.Join(dir, "explicit"), map[string]any{
		"retention": map[string]any{
			"default_days_by_classification":   map[string]any{"restricted": 30},
			"refuse_restricted_without_window": true,
		},
	})
	_, _, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected a refusal for a configured 'restricted' retention default")
	}
	if !strings.Contains(err.Error(), "restricted") {
		t.Errorf("error should name the rejected key, got: %v", err)
	}
}

// TestLoadConfigRefusesRetentionDefaultsThatAreNotClassifications: a typo'd
// key would otherwise be silently unreachable, which reads as "no window
// configured" for the classification the author meant.
func TestLoadConfigRefusesRetentionDefaultsThatAreNotClassifications(t *testing.T) {
	dir := isolatedProject(t)
	path := writeConfigFile(t, filepath.Join(dir, "explicit"), map[string]any{
		"retention": map[string]any{
			"default_days_by_classification": map[string]any{"internel": 30},
		},
	})
	if _, _, err := LoadConfig(path); err == nil {
		t.Fatal("expected a refusal for an unrecognised retention classification key")
	}
}

// TestResolveRetentionUntilRefusesRestrictedWithoutAWindow is the guarantee
// SECURITY.md states outright: restricted is the one tier where "kept
// forever because nobody decided" must not be reachable by omission.
func TestResolveRetentionUntilRefusesRestrictedWithoutAWindow(t *testing.T) {
	dir := isolatedProject(t)
	cfg, _, err := LoadConfig(writeConfigFile(t, filepath.Join(dir, "explicit"), map[string]any{}))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	until, err := ResolveRetentionUntil(cfg, "restricted", nil)
	if err == nil {
		t.Fatalf("restricted content ingested with retention %v and no explicit window", until)
	}
	if !strings.Contains(err.Error(), "--retention-days") {
		t.Errorf("error should say how to supply a window, got: %v", err)
	}
	if until != nil {
		t.Error("a refused retention resolution must return no window")
	}

	// Control: an explicit window is accepted for restricted.
	days := 30
	until, err = ResolveRetentionUntil(cfg, "restricted", &days)
	if err != nil {
		t.Fatalf("restricted with an explicit window was refused: %v", err)
	}
	if until == nil || *until == "" {
		t.Error("an explicit window must produce a recorded retention_until")
	}
}

// TestResolveRetentionUntilRefusesANonPositiveWindow: "0 days" and "-1 days"
// are not windows, and must not be accepted and then rounded into one.
func TestResolveRetentionUntilRefusesANonPositiveWindow(t *testing.T) {
	dir := isolatedProject(t)
	cfg, _, err := LoadConfig(writeConfigFile(t, filepath.Join(dir, "explicit"), map[string]any{}))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	for _, days := range []int{0, -1, -365} {
		value := days
		if _, err := ResolveRetentionUntil(cfg, "internal", &value); err == nil {
			t.Errorf("--retention-days %d was accepted", days)
		}
	}
}

// TestResolveRetentionUntilDefaultsToIndefinite pins the shipped placeholder:
// every configured classification is indefinite on purpose, and that is a
// recorded "no window has been decided", not an assertion that content
// should be kept forever.
func TestResolveRetentionUntilDefaultsToIndefinite(t *testing.T) {
	dir := isolatedProject(t)
	cfg, _, err := LoadConfig(writeConfigFile(t, filepath.Join(dir, "explicit"), map[string]any{}))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	for _, classification := range []string{"public", "internal", "confidential"} {
		until, err := ResolveRetentionUntil(cfg, classification, nil)
		if err != nil {
			t.Fatalf("ResolveRetentionUntil(%s): %v", classification, err)
		}
		if until != nil {
			t.Errorf("%s resolved to a window (%q) no configuration set", classification, *until)
		}
	}
}

// TestResolveRetentionUntilHonoursAConfiguredWindow is the control for the
// test above: the defaults are indefinite because nothing configured them,
// not because configuring them does nothing.
func TestResolveRetentionUntilHonoursAConfiguredWindow(t *testing.T) {
	dir := isolatedProject(t)
	path := writeConfigFile(t, filepath.Join(dir, "explicit"), map[string]any{
		"retention": map[string]any{
			"default_days_by_classification": map[string]any{"internal": 7},
		},
	})
	cfg, _, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	until, err := ResolveRetentionUntil(cfg, "internal", nil)
	if err != nil {
		t.Fatalf("ResolveRetentionUntil: %v", err)
	}
	if until == nil {
		t.Fatal("a configured window resolved to indefinite")
	}
	if *until <= nowISO() {
		t.Errorf("retention_until %q is not in the future", *until)
	}
}

// TestResolveRetentionUntilRestrictedFallsThroughOnlyWhenRefusalIsDisabled:
// the refusal is a setting, and turning it off is the one way restricted
// content reaches indefinite -- deliberately, by a config author, not by
// omission at the command line.
func TestResolveRetentionUntilRestrictedFallsThroughOnlyWhenRefusalIsDisabled(t *testing.T) {
	dir := isolatedProject(t)
	path := writeConfigFile(t, filepath.Join(dir, "explicit"), map[string]any{
		"retention": map[string]any{"refuse_restricted_without_window": false},
	})
	cfg, _, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	until, err := ResolveRetentionUntil(cfg, "restricted", nil)
	if err != nil {
		t.Fatalf("ResolveRetentionUntil: %v", err)
	}
	if until != nil {
		t.Errorf("expected indefinite, got %q", *until)
	}
}
