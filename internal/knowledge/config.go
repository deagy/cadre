package knowledge

// config.go ports roster/knowledge-store/src/config.py's configuration
// resolution and roster/knowledge-store/src/service.py's classification and
// retention validation.
//
// Three things live here, and they are here together because they are one
// decision, not three:
//
//  1. Which store a command talks to. Resolution has three tiers -- an
//     explicit --config path, a project-local
//     .agents/knowledge-store/config.json discovered by walking up to the
//     project's .git boundary, and a shared global fallback under
//     $KNOWLEDGE_STORE_HOME (default ~/.agents/knowledge-store). The global
//     tier is one database shared by every project on the machine that has
//     not declared its own partition; that is deliberate (see
//     roster/knowledge-store/SECURITY.md, "Global default, project-local
//     override") and it is exactly why scope discipline on retrieval is not
//     optional.
//
//  2. Fail-closed on a named-but-missing config. An explicit --config that
//     does not resolve to a file is an error, never a silently created empty
//     store. Before this existed the Go CLI read --config as a *database*
//     path and handed it to knowledge.Open, which creates directories and an
//     empty schema -- so a typo produced a working, empty, wrong store
//     instead of a refusal.
//
//  3. What a classification and a retention window are allowed to be.
//     Classification is exact-match against four labels; anything else is
//     refused rather than stored. `restricted` content requires an explicit
//     retention window because it is the one tier where "nobody decided"
//     must not be indistinguishable from "keep forever".
//
// Deviations from config.py, all deliberate:
//
//   - The default database is "./store.db", not "./data/knowledge.db". This
//     Go implementation's own docs (internal/knowledge/SCHEMA.md,
//     ARCHITECTURE.md) document `.agents/knowledge-store/store.db` as the
//     default location, and a project-local config.json resolves to exactly
//     that path with this default.
//   - The embedding provider names are this package's ("local-hashing",
//     "openai-compatible"), not Python's ("hashing", "openai-compatible"),
//     and the default dimension is 128, matching NewLocalHashingEmbedder's
//     own default. Vectors are only comparable within one provider/model/
//     dimension identity (see search.go), so changing these defaults would
//     silently orphan every already-ingested chunk.
//   - ingestion.default_classification is validated at load time. config.py
//     leaves it unchecked, which defers the failure to whichever ingest
//     first relies on the default.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/deagy/cadre/cli/internal/config"
	"github.com/deagy/cadre/cli/internal/platform"
)

// Classifications are the four accepted classification labels, narrowest
// audience last. Matching is exact: SECURITY.md states plainly that
// "classification filtering is exact-match, not hierarchical".
var Classifications = []string{"public", "internal", "confidential", "restricted"}

// SupportedEmbeddingProviders are the provider identifiers a config may name.
var SupportedEmbeddingProviders = []string{"local-hashing", "openai-compatible"}

// LegacyLocalEmbeddingProvider is the name the Python implementation this
// package replaced used for the offline provider. That implementation
// accepted {"hashing", "openai-compatible"}; the port renamed the first to
// "local-hashing" and shipped no migration, so every config written before
// it -- including the default config the Python implementation itself wrote
// at ~/.agents/knowledge-store/config.json -- stopped loading with
// `unsupported embedding provider: "hashing"`, taking every `cadre
// knowledge` command with it.
const LegacyLocalEmbeddingProvider = "hashing"

// canonicalEmbeddingProvider maps the legacy spelling onto this package's.
//
// Normalising here, rather than adding "hashing" to
// SupportedEmbeddingProviders, is deliberate. The implicit-project-config
// guard compares the provider against "local-hashing" exactly, to stop a
// clonable project-local file deciding that this project's content leaves
// the machine. Widening the accepted list would leave a config naming the
// legacy spelling failing that guard as though it had asked for remote
// embeddings -- rejected for entirely the wrong reason, with a message
// about remote embeddings for a config that named the offline provider.
// One normalisation point keeps validation, the trust-scope guard, and the
// stored value seeing the same canonical name.
func canonicalEmbeddingProvider(raw any) string {
	provider, _ := raw.(string)
	if provider == LegacyLocalEmbeddingProvider {
		return "local-hashing"
	}
	return provider
}

// Config-resolution tiers. Exposed so a caller can gate behaviour on the
// shared tier specifically without altering resolution order.
const (
	TierExplicitConfig = "explicit-config"
	TierProjectLocal   = "project-local"
	TierGlobalFallback = "global-fallback"
)

// ProjectLocalRelativePath is where a project-local config lives, relative
// to a discovered project root.
var ProjectLocalRelativePath = filepath.Join(".agents", "knowledge-store", "config.json")

// EmbeddingConfig mirrors config.py's embedding section.
type EmbeddingConfig struct {
	Provider       string  `json:"provider"`
	Model          string  `json:"model"`
	Dimensions     int     `json:"dimensions"`
	BaseURL        string  `json:"base_url,omitempty"`
	APIKeyEnv      string  `json:"api_key_env"`
	BatchSize      int     `json:"batch_size"`
	TimeoutSeconds float64 `json:"timeout_seconds"`
}

// ChunkingConfig mirrors config.py's chunking section.
type ChunkingConfig struct {
	MaxCharacters     int `json:"max_characters"`
	OverlapCharacters int `json:"overlap_characters"`
}

// IngestionConfig mirrors config.py's ingestion section.
type IngestionConfig struct {
	DefaultClassification string `json:"default_classification"`
	RedactSecrets         bool   `json:"redact_secrets"`
}

// RetentionConfig mirrors config.py's retention section.
//
// A nil value in DefaultDaysByClassification means indefinite -- no window
// recorded. Every shipped default is nil on purpose: concrete windows are an
// open Product Owner / Engineering Lead decision, and shipping working day
// counts ahead of it would let them become policy by default inertia.
// Indefinite records "no window has been decided" rather than asserting one
// nobody chose.
//
// `restricted` is deliberately absent from that map and rejected if a config
// supplies it, because ResolveRetentionUntil never reads a restricted entry:
// restricted either refuses or falls through to indefinite, controlled only
// by RefuseRestrictedWithoutWindow. A configured restricted default would be
// dead configuration that looks load-bearing.
type RetentionConfig struct {
	DefaultDaysByClassification   map[string]*int `json:"default_days_by_classification"`
	RefuseRestrictedWithoutWindow bool            `json:"refuse_restricted_without_window"`
}

// Config is the fully resolved, validated knowledge-store configuration.
type Config struct {
	Database  string          `json:"database"`
	Embedding EmbeddingConfig `json:"embedding"`
	Chunking  ChunkingConfig  `json:"chunking"`
	Ingestion IngestionConfig `json:"ingestion"`
	Retention RetentionConfig `json:"retention"`
}

// ConfigError is a caller-facing configuration or validation failure. The
// CLI renders these as clean errors.
type ConfigError struct{ msg string }

func (e *ConfigError) Error() string { return e.msg }

func configErrorf(format string, args ...any) error {
	return &ConfigError{msg: fmt.Sprintf(format, args...)}
}

// ValidateClassification refuses anything that is not one of the four
// labels, rather than storing it.
//
// This matters more on the write path than the read path: content ingested
// under an unrecognised label is content no retrieval policy describes. It
// is not "less exposed" for being unrecognised -- a caller asserting that
// same unrecognised string reaches it, and nothing about the label tells a
// reviewer what handling it requires.
func ValidateClassification(classification string) (string, error) {
	for _, known := range Classifications {
		if classification == known {
			return classification, nil
		}
	}
	return "", configErrorf(
		"invalid classification: %q. Expected one of: %s.",
		classification, strings.Join(Classifications, ", "))
}

func defaultRawConfig() map[string]any {
	return map[string]any{
		"database": "./store.db",
		"embedding": map[string]any{
			"provider":        "local-hashing",
			"model":           "",
			"dimensions":      128.0,
			"base_url":        nil,
			"api_key_env":     "KNOWLEDGE_EMBEDDING_API_KEY",
			"batch_size":      32.0,
			"timeout_seconds": 30.0,
		},
		"chunking":  map[string]any{"max_characters": 2400.0, "overlap_characters": 240.0},
		"ingestion": map[string]any{"default_classification": "internal", "redact_secrets": true},
		"retention": map[string]any{
			"default_days_by_classification": map[string]any{
				"internal": nil, "confidential": nil, "public": nil,
			},
			"refuse_restricted_without_window": true,
		},
	}
}

// mergeMaps deep-merges override over base, mirroring config.py's _merge.
func mergeMaps(base, override map[string]any) map[string]any {
	result := make(map[string]any, len(base))
	for k, v := range base {
		result[k] = v
	}
	for key, value := range override {
		if overrideMap, ok := value.(map[string]any); ok {
			if baseMap, ok := result[key].(map[string]any); ok {
				result[key] = mergeMaps(baseMap, overrideMap)
				continue
			}
		}
		result[key] = value
	}
	return result
}

// FindProjectLocalConfig walks upward from start for
// .agents/knowledge-store/config.json, stopping at the first .git boundary
// so a config above the project root is never picked up. An empty start
// means the current working directory.
func FindProjectLocalConfig(start string) (string, bool) {
	return platform.FindFileAtProjectRoot(ProjectLocalRelativePath, start)
}

// DefaultConfigPath resolves the implicit config location: a project-local
// config first, else knowledge_store.home (a global-only setting, since it
// picks where a database is read and written and a project-local settings
// file arrives with `git clone`), else ~/.agents/knowledge-store.
func DefaultConfigPath() (string, error) {
	if path, ok := FindProjectLocalConfig(""); ok {
		return path, nil
	}
	home, err := config.ResolveOptional("knowledge_store.home", "")
	if err != nil {
		return "", err
	}
	var base string
	if s, ok := home.(string); ok && strings.TrimSpace(s) != "" {
		expanded, err := expandUser(s)
		if err != nil {
			return "", err
		}
		base = expanded
	} else {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(homeDir, ".agents", "knowledge-store")
	}
	return filepath.Join(base, "config.json"), nil
}

func expandUser(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			return homeDir, nil
		}
		return filepath.Join(homeDir, path[2:]), nil
	}
	return path, nil
}

func asConfigInt(value any) (int, bool) {
	switch v := value.(type) {
	case float64:
		if v != float64(int(v)) {
			return 0, false
		}
		return int(v), true
	case int:
		return v, true
	default:
		return 0, false
	}
}

func positiveConfigInteger(value any, name string, minimum int) error {
	n, ok := asConfigInt(value)
	if !ok || n < minimum {
		return configErrorf("%s must be an integer of at least %d", name, minimum)
	}
	return nil
}

// LoadConfig loads and validates configuration, failing closed when an
// explicit path does not exist. Returns the resolved config and the tier it
// was resolved from.
func LoadConfig(configPath string) (*Config, string, error) {
	var selected, tier string
	var implicitProjectConfig bool

	if configPath != "" {
		abs, err := filepath.Abs(configPath)
		if err != nil {
			return nil, "", err
		}
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			// Fail closed. The alternative -- treating a missing path as
			// "use defaults" -- turns a typo into a brand new empty store
			// that reports zero results rather than an error.
			return nil, "", configErrorf("explicit config file does not exist: %s", abs)
		}
		selected = abs
		tier = TierExplicitConfig
	} else {
		path, err := DefaultConfigPath()
		if err != nil {
			return nil, "", err
		}
		selected = path
		projectLocal, ok := FindProjectLocalConfig("")
		implicitProjectConfig = ok && sameConfigPath(projectLocal, selected)
		if implicitProjectConfig {
			tier = TierProjectLocal
		} else {
			tier = TierGlobalFallback
		}
	}

	supplied := map[string]any{}
	if info, err := os.Stat(selected); err == nil && !info.IsDir() {
		data, err := os.ReadFile(selected)
		if err != nil {
			return nil, "", err
		}
		var loaded any
		if err := json.Unmarshal(data, &loaded); err != nil {
			return nil, "", configErrorf("cannot parse %s as JSON: %v", selected, err)
		}
		m, ok := loaded.(map[string]any)
		if !ok {
			return nil, "", configErrorf("configuration root must be a JSON object")
		}
		supplied = m
	}

	raw := mergeMaps(defaultRawConfig(), supplied)
	for _, section := range []string{"embedding", "chunking", "ingestion", "retention"} {
		if _, ok := raw[section].(map[string]any); !ok {
			return nil, "", configErrorf("%s must be a JSON object", section)
		}
	}

	databaseStr, _ := raw["database"].(string)
	if strings.TrimSpace(databaseStr) == "" {
		return nil, "", configErrorf("database must be a non-empty string")
	}
	baseDirectory := filepath.Dir(selected)
	resolvedDatabase := databaseStr
	if !filepath.IsAbs(resolvedDatabase) {
		resolvedDatabase = filepath.Join(baseDirectory, resolvedDatabase)
	}
	resolvedDatabase, err := filepath.Abs(resolvedDatabase)
	if err != nil {
		return nil, "", err
	}
	if implicitProjectConfig && !config.IsSameOrDescendant(resolvedDatabase, baseDirectory) {
		return nil, "", configErrorf(
			"project-local knowledge-store database must remain under its config directory")
	}

	embeddingRaw := raw["embedding"].(map[string]any)
	provider := canonicalEmbeddingProvider(embeddingRaw["provider"])
	if !stringInList(provider, SupportedEmbeddingProviders) {
		return nil, "", configErrorf("unsupported embedding provider: %q. Expected one of: %s.",
			provider, strings.Join(SupportedEmbeddingProviders, ", "))
	}
	if implicitProjectConfig && provider != "local-hashing" {
		// A project-local config.json arrives with `git clone` and is
		// editable by anyone who can open a pull request, so it must not be
		// able to decide that this project's content leaves the machine.
		return nil, "", configErrorf(
			"project-local configuration cannot enable remote embeddings")
	}
	if err := positiveConfigInteger(embeddingRaw["dimensions"], "embedding.dimensions", 32); err != nil {
		return nil, "", err
	}
	dimensions, _ := asConfigInt(embeddingRaw["dimensions"])
	if err := positiveConfigInteger(embeddingRaw["batch_size"], "embedding.batch_size", 1); err != nil {
		return nil, "", err
	}
	batchSize, _ := asConfigInt(embeddingRaw["batch_size"])
	timeout, timeoutOK := embeddingRaw["timeout_seconds"].(float64)
	if !timeoutOK {
		if n, ok := asConfigInt(embeddingRaw["timeout_seconds"]); ok {
			timeout, timeoutOK = float64(n), true
		}
	}
	if !timeoutOK || timeout <= 0 || timeout > 300 {
		return nil, "", configErrorf("embedding.timeout_seconds must be greater than 0 and at most 300")
	}
	model, _ := embeddingRaw["model"].(string)
	baseURL, _ := embeddingRaw["base_url"].(string)
	apiKeyEnv, _ := embeddingRaw["api_key_env"].(string)

	chunkingRaw := raw["chunking"].(map[string]any)
	if err := positiveConfigInteger(chunkingRaw["max_characters"], "chunking.max_characters", 1); err != nil {
		return nil, "", err
	}
	maxChars, _ := asConfigInt(chunkingRaw["max_characters"])
	overlap, overlapOK := asConfigInt(chunkingRaw["overlap_characters"])
	if !overlapOK || overlap < 0 {
		return nil, "", configErrorf("chunking.overlap_characters must be a non-negative integer")
	}
	if overlap >= maxChars {
		return nil, "", configErrorf("chunk overlap must be smaller than max_characters")
	}

	ingestionRaw := raw["ingestion"].(map[string]any)
	redactSecrets, ok := ingestionRaw["redact_secrets"].(bool)
	if !ok {
		return nil, "", configErrorf("ingestion.redact_secrets must be a boolean")
	}
	defaultClassification, _ := ingestionRaw["default_classification"].(string)
	if _, err := ValidateClassification(defaultClassification); err != nil {
		return nil, "", configErrorf(
			"ingestion.default_classification is not a recognised classification: %v", err)
	}

	retentionRaw := raw["retention"].(map[string]any)
	byClassificationRaw, ok := retentionRaw["default_days_by_classification"].(map[string]any)
	if !ok {
		return nil, "", configErrorf("retention.default_days_by_classification must be a JSON object")
	}
	if _, present := byClassificationRaw["restricted"]; present {
		return nil, "", configErrorf(
			"retention.default_days_by_classification must not set 'restricted': restricted " +
				"content's retention window is controlled only by " +
				"retention.refuse_restricted_without_window and an explicit --retention-days at " +
				"ingest time, never by a configured default -- a 'restricted' entry here would be " +
				"silently ignored, so it is rejected instead.")
	}
	byClassification := map[string]*int{}
	for classification, value := range byClassificationRaw {
		if _, err := ValidateClassification(classification); err != nil {
			return nil, "", configErrorf(
				"retention.default_days_by_classification names an unrecognised classification %q",
				classification)
		}
		if value == nil {
			byClassification[classification] = nil
			continue
		}
		name := "retention.default_days_by_classification." + classification
		if err := positiveConfigInteger(value, name, 1); err != nil {
			return nil, "", err
		}
		days, _ := asConfigInt(value)
		daysCopy := days
		byClassification[classification] = &daysCopy
	}
	refuseRestricted, ok := retentionRaw["refuse_restricted_without_window"].(bool)
	if !ok {
		return nil, "", configErrorf("retention.refuse_restricted_without_window must be a boolean")
	}

	cfg := &Config{
		Database: resolvedDatabase,
		Embedding: EmbeddingConfig{
			Provider: provider, Model: model, Dimensions: dimensions,
			BaseURL: baseURL, APIKeyEnv: apiKeyEnv,
			BatchSize: batchSize, TimeoutSeconds: timeout,
		},
		Chunking:  ChunkingConfig{MaxCharacters: maxChars, OverlapCharacters: overlap},
		Ingestion: IngestionConfig{DefaultClassification: defaultClassification, RedactSecrets: redactSecrets},
		Retention: RetentionConfig{
			DefaultDaysByClassification:   byClassification,
			RefuseRestrictedWithoutWindow: refuseRestricted,
		},
	}
	return cfg, tier, nil
}

// ResolveRetentionUntil resolves the retention window ingest records for a
// message, or nil for indefinite.
//
// `restricted` is refused here rather than silently defaulted when
// RefuseRestrictedWithoutWindow is set (the default): the most sensitive
// classification is exactly the one where "nobody decided a retention
// window" must not be indistinguishable from "kept indefinitely on
// purpose". An explicit override always wins, for every classification,
// including restricted.
func ResolveRetentionUntil(cfg *Config, classification string, retentionDaysOverride *int) (*string, error) {
	if retentionDaysOverride != nil {
		if *retentionDaysOverride < 1 {
			return nil, configErrorf("--retention-days must be a positive integer number of days")
		}
		return daysFromNow(*retentionDaysOverride), nil
	}

	if classification == "restricted" {
		if cfg.Retention.RefuseRestrictedWithoutWindow {
			return nil, configErrorf(
				"restricted content requires an explicit retention window: pass " +
					"--retention-days <n>. restricted has no configured default precisely " +
					"so an unresolved retention decision cannot be mistaken for a deliberate " +
					"indefinite one.")
		}
		return nil, nil
	}

	days, present := cfg.Retention.DefaultDaysByClassification[classification]
	if !present || days == nil {
		return nil, nil
	}
	return daysFromNow(*days), nil
}

func daysFromNow(days int) *string {
	until := time.Now().UTC().AddDate(0, 0, days).Format("2006-01-02T15:04:05.000Z")
	return &until
}

func stringInList(s string, list []string) bool {
	for _, item := range list {
		if s == item {
			return true
		}
	}
	return false
}

func sameConfigPath(a, b string) bool {
	aAbs, errA := filepath.Abs(a)
	bAbs, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return aAbs == bAbs
}
