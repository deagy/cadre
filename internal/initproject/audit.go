// audit.go ports init_project.py's B-006/THREAT-MODEL-HARDENING-3/4 audit
// logging: an append-only JSONL trail of every accepted/rejected/written
// decision this package makes, with rejected/redacted values reaching the
// log only as a sha256 hash, never in cleartext.
package initproject

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// DefaultAuditLogPath resolves the audit log location: AGENTS_INIT_AUDIT_LOG
// if set, else $XDG_CACHE_HOME/agents-init/audit.jsonl (or
// ~/.cache/agents-init/audit.jsonl).
func DefaultAuditLogPath() string {
	if override := os.Getenv("AGENTS_INIT_AUDIT_LOG"); override != "" {
		return override
	}
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "agents-init", "audit.jsonl")
}

// AuditEntry is one line of the audit log.
type AuditEntry struct {
	Timestamp   string `json:"timestamp"`
	Kind        string `json:"kind"` // "accepted" | "rejected" | "written"
	Category    string `json:"category"`
	Context     string `json:"context"`
	Detail      string `json:"detail"`
	Value       string `json:"value,omitempty"`
	ValueSHA256 string `json:"value_sha256,omitempty"`
}

// NewAuditEntry builds one audit entry. If hashOnly is true, or kind is
// "rejected", value (when non-empty) is recorded only as its sha256 hash,
// never in cleartext.
func NewAuditEntry(kind, category, context, detail, value string, hashOnly bool) AuditEntry {
	entry := AuditEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Kind:      kind,
		Category:  category,
		Context:   context,
		Detail:    detail,
	}
	if value != "" {
		if hashOnly || kind == "rejected" {
			entry.ValueSHA256 = sha256Hex(value)
		} else {
			entry.Value = value
		}
	}
	return entry
}

// AppendAuditEntries appends entries to path (or DefaultAuditLogPath if
// path is ""), creating parent directories as needed. Returns the path
// written to.
func AppendAuditEntries(entries []AuditEntry, path string) (string, error) {
	if path == "" {
		path = DefaultAuditLogPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	for _, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			return "", err
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			return "", err
		}
	}
	return path, nil
}
