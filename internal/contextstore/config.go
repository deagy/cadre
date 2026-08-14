// Package contextstore ports roster/context-store/src/ (config.py,
// handles.py, database.py, service.py, export.py, cli.py): the agent
// context store, a local-first place for an agent to park working material
// outside its own context window and get it back later by handle.
//
// This is NOT the knowledge store (internal/knowledge), and the difference
// is the entire point -- see roster/context-store/README.md's comparison
// table. Content here is agent-written working material, never
// steward-dispositioned, always expires (there is no indefinite entry),
// and this package must never import internal/knowledge or vice versa
// (mirroring roster/orchestration/test/test_context_boundary.py's
// structural assertion on the Python side, which this Go port has no
// equivalent automated guard for yet -- the separation is preserved here by
// convention: grep internal/contextstore for "internal/knowledge" before
// merging any change to this package).
//
// Read roster/context-store/SECURITY.md before trusting any filter this
// package applies as an access-control mechanism: scope, classification,
// and source are caller-asserted and unauthenticated on the CLI, exactly as
// upstream states.
package contextstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/deagy/cadre/cli/internal/config"
	"github.com/deagy/cadre/cli/internal/platform"
)

// Scopes are the three entry scopes, narrowest to widest audience.
var Scopes = []string{"agent", "dispatch", "project"}

// Classifications are the four accepted classification labels.
var Classifications = []string{"public", "internal", "confidential", "restricted"}

// SupportedEmbeddingProviders is deliberately a single-element list.
// Extending it is not a configuration change; it is a decision about
// whether unreviewed agent working material may leave the machine (OD-5).
var SupportedEmbeddingProviders = []string{"hashing"}

const (
	TierExplicitConfig = "explicit-config"
	TierProjectLocal   = "project-local"
	TierGlobalFallback = "global-fallback"
)

// ProjectLocalRelativePath is where a project-local config lives, relative
// to a discovered project root.
var ProjectLocalRelativePath = filepath.Join(".agents", "context-store", "config.json")

// Expiry mirrors config.py's expiry section.
type Expiry struct {
	DefaultTTLDaysByScope map[string]int `json:"default_ttl_days_by_scope"`
	MaximumTTLDays        int            `json:"maximum_ttl_days"`
}

// Embedding mirrors config.py's embedding section.
type Embedding struct {
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Dimensions int    `json:"dimensions"`
}

// Chunking mirrors config.py's chunking section.
type Chunking struct {
	MaxCharacters     int `json:"max_characters"`
	OverlapCharacters int `json:"overlap_characters"`
}

// Config is the fully resolved, validated context-store configuration.
type Config struct {
	Database  string         `json:"database"`
	Ingestion map[string]any `json:"ingestion"`
	Expiry    Expiry         `json:"expiry"`
	Limits    map[string]any `json:"limits"`
	Chunking  Chunking       `json:"chunking"`
	Embedding Embedding      `json:"embedding"`
}

// RedactSecrets reads ingestion.redact_secrets.
func (c *Config) RedactSecrets() bool {
	v, _ := c.Ingestion["redact_secrets"].(bool)
	return v
}

// MaxEntryBytes reads limits.max_entry_bytes.
func (c *Config) MaxEntryBytes() int {
	switch v := c.Limits["max_entry_bytes"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

func defaultRawConfig() map[string]any {
	return map[string]any{
		"database":  "./data/context.db",
		"ingestion": map[string]any{"redact_secrets": true},
		"expiry": map[string]any{
			"default_ttl_days_by_scope": map[string]any{"agent": 1.0, "dispatch": 7.0, "project": 30.0},
			"maximum_ttl_days":          90.0,
		},
		"limits":   map[string]any{"max_entry_bytes": 1048576.0},
		"chunking": map[string]any{"max_characters": 2400.0, "overlap_characters": 240.0},
		"embedding": map[string]any{
			"provider": "hashing", "model": "feature-hash-v1", "dimensions": 384.0,
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
// .agents/context-store/config.json, stopping at the first .git boundary.
func FindProjectLocalConfig(start string) (string, bool) {
	return platform.FindFileAtProjectRoot(ProjectLocalRelativePath, start)
}

// DefaultConfigPath resolves the implicit config location: project-local
// first, else context_store.home (global-only), else ~/.agents/context-store.
func DefaultConfigPath() (string, error) {
	if path, ok := FindProjectLocalConfig(""); ok {
		return path, nil
	}
	home, err := config.ResolveOptional("context_store.home", "")
	if err != nil {
		return "", err
	}
	var base string
	if s, ok := home.(string); ok && s != "" {
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
		base = filepath.Join(homeDir, ".agents", "context-store")
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

func positiveInteger(value any, name string, minimum int) error {
	n, ok := asInt(value)
	if !ok || n < minimum {
		return fmt.Errorf("%s must be an integer of at least %d", name, minimum)
	}
	return nil
}

func asInt(value any) (int, bool) {
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

// LoadConfig loads and validates configuration, failing closed when an
// explicit path does not exist. Returns the resolved config and the tier
// it was resolved from.
func LoadConfig(configPath string) (*Config, string, error) {
	var selected string
	var tier string
	var implicitProjectConfig bool

	if configPath != "" {
		abs, err := filepath.Abs(configPath)
		if err != nil {
			return nil, "", err
		}
		if info, err := os.Stat(abs); err != nil || info.IsDir() {
			return nil, "", fmt.Errorf("explicit config file does not exist: %s", abs)
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
		implicitProjectConfig = ok && samePath(projectLocal, selected)
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
			return nil, "", err
		}
		m, ok := loaded.(map[string]any)
		if !ok {
			return nil, "", fmt.Errorf("configuration root must be a JSON object")
		}
		supplied = m
	}

	raw := mergeMaps(defaultRawConfig(), supplied)
	for _, section := range []string{"ingestion", "expiry", "limits", "chunking", "embedding"} {
		if _, ok := raw[section].(map[string]any); !ok {
			return nil, "", fmt.Errorf("%s must be a JSON object", section)
		}
	}
	databaseStr, _ := raw["database"].(string)
	if strings.TrimSpace(databaseStr) == "" {
		return nil, "", fmt.Errorf("database must be a non-empty string")
	}

	baseDirectory := filepath.Dir(selected)
	var resolvedDatabase string
	if filepath.IsAbs(databaseStr) {
		abs, err := filepath.Abs(databaseStr)
		if err != nil {
			return nil, "", err
		}
		resolvedDatabase = abs
	} else {
		abs, err := filepath.Abs(filepath.Join(baseDirectory, databaseStr))
		if err != nil {
			return nil, "", err
		}
		resolvedDatabase = abs
	}
	if implicitProjectConfig && !config.IsSameOrDescendant(resolvedDatabase, baseDirectory) {
		return nil, "", fmt.Errorf("project-local context-store database must remain under its config directory")
	}

	expiryRaw := raw["expiry"].(map[string]any)
	byScopeRaw, ok := expiryRaw["default_ttl_days_by_scope"].(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("expiry.default_ttl_days_by_scope must be a JSON object")
	}
	byScope := map[string]int{}
	for _, scope := range Scopes {
		v, present := byScopeRaw[scope]
		if !present {
			return nil, "", fmt.Errorf("expiry.default_ttl_days_by_scope must set a window for scope '%s'", scope)
		}
		if err := positiveInteger(v, "expiry.default_ttl_days_by_scope."+scope, 1); err != nil {
			return nil, "", err
		}
		n, _ := asInt(v)
		byScope[scope] = n
	}
	var unknownScopes []string
	for k := range byScopeRaw {
		if !stringInSlice(k, Scopes) {
			unknownScopes = append(unknownScopes, k)
		}
	}
	if len(unknownScopes) > 0 {
		sortStrings(unknownScopes)
		return nil, "", fmt.Errorf(
			"expiry.default_ttl_days_by_scope has unknown scope(s): %s. Known scopes: %s",
			strings.Join(unknownScopes, ", "), strings.Join(Scopes, ", "))
	}
	if err := positiveInteger(expiryRaw["maximum_ttl_days"], "expiry.maximum_ttl_days", 1); err != nil {
		return nil, "", err
	}
	maxTTL, _ := asInt(expiryRaw["maximum_ttl_days"])
	for _, scope := range Scopes {
		if byScope[scope] > maxTTL {
			return nil, "", fmt.Errorf(
				"expiry.default_ttl_days_by_scope.%s (%d) exceeds expiry.maximum_ttl_days (%d)",
				scope, byScope[scope], maxTTL)
		}
	}

	limitsRaw := raw["limits"].(map[string]any)
	if err := positiveInteger(limitsRaw["max_entry_bytes"], "limits.max_entry_bytes", 1024); err != nil {
		return nil, "", err
	}

	ingestionRaw := raw["ingestion"].(map[string]any)
	if _, ok := ingestionRaw["redact_secrets"].(bool); !ok {
		return nil, "", fmt.Errorf("ingestion.redact_secrets must be a boolean")
	}

	chunkingRaw := raw["chunking"].(map[string]any)
	if err := positiveInteger(chunkingRaw["max_characters"], "chunking.max_characters", 1); err != nil {
		return nil, "", err
	}
	maxChars, _ := asInt(chunkingRaw["max_characters"])
	overlapVal, overlapOK := asInt(chunkingRaw["overlap_characters"])
	if !overlapOK || overlapVal < 0 {
		return nil, "", fmt.Errorf("chunking.overlap_characters must be a non-negative integer")
	}
	if overlapVal >= maxChars {
		return nil, "", fmt.Errorf("chunk overlap must be smaller than max_characters")
	}

	embeddingRaw := raw["embedding"].(map[string]any)
	provider, _ := embeddingRaw["provider"].(string)
	if !stringInSlice(provider, SupportedEmbeddingProviders) {
		return nil, "", fmt.Errorf(
			"unsupported embedding provider for the context store: %q. Only %s is accepted. "+
				"Remote providers are refused here on purpose: entries are unreviewed agent working "+
				"material, and whether it may be transmitted to a third-party endpoint is an open "+
				"security decision (OD-5). This is not a limitation to work around by editing "+
				"configuration -- the module that would perform a remote embedding is not importable "+
				"from this store at all", provider, strings.Join(SupportedEmbeddingProviders, ", "))
	}
	if err := positiveInteger(embeddingRaw["dimensions"], "embedding.dimensions", 32); err != nil {
		return nil, "", err
	}
	dimensions, _ := asInt(embeddingRaw["dimensions"])
	model, _ := embeddingRaw["model"].(string)
	if strings.TrimSpace(model) == "" {
		return nil, "", fmt.Errorf("embedding.model must be a non-empty string")
	}

	cfg := &Config{
		Database:  resolvedDatabase,
		Ingestion: ingestionRaw,
		Expiry:    Expiry{DefaultTTLDaysByScope: byScope, MaximumTTLDays: maxTTL},
		Limits:    limitsRaw,
		Chunking:  Chunking{MaxCharacters: maxChars, OverlapCharacters: overlapVal},
		Embedding: Embedding{Provider: provider, Model: model, Dimensions: dimensions},
	}
	return cfg, tier, nil
}

func stringInSlice(s string, list []string) bool {
	for _, item := range list {
		if s == item {
			return true
		}
	}
	return false
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

func samePath(a, b string) bool {
	aAbs, errA := filepath.Abs(a)
	bAbs, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return aAbs == bAbs
}
