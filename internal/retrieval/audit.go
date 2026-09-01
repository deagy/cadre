package retrieval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/deagy/recall/govern"
)

// AuditRow is one recorded retrieval.
//
// Embedder and Model are on every row because a retrieval is only
// reproducible against the model that produced the vectors it searched. The
// engine this replaced refused a search with no embedding provider for that
// reason; recall takes its embedder at construction, so the identity is
// required once when the governed view is built and recorded on every row
// afterwards.
type AuditRow struct {
	RecordedAt     string   `json:"recorded_at"`
	QueryID        string   `json:"query_id"`
	Classification string   `json:"classification"`
	SourceFilters  []string `json:"source_filters"`
	AllSources     bool     `json:"all_sources"`
	Agent          string   `json:"agent,omitempty"`
	TaskID         string   `json:"task_id,omitempty"`
	ResultCount    int      `json:"result_count"`
	Embedder       string   `json:"embedder"`
	Model          string   `json:"model"`
}

// AuditLog appends one line per completed retrieval.
//
// The query text is not written: the row carries the same stable query id the
// bundle carries, so an audit row and the bundle it produced can be
// correlated without the log becoming a second copy of what people searched
// for.
//
// A write that fails is returned as an error rather than swallowed. govern
// fails the retrieval when recording fails, which is the point: results the
// system cannot account for would make the audit advisory.
type AuditLog struct {
	mu   sync.Mutex
	path string
}

// NewAuditLog returns a log appending to path, creating its directory.
func NewAuditLog(path string) (*AuditLog, error) {
	if path == "" {
		return nil, fmt.Errorf("retrieval: an audit log path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("retrieval: cannot create audit log directory: %w", err)
	}
	return &AuditLog{path: path}, nil
}

// Path is where rows are appended.
func (l *AuditLog) Path() string { return l.path }

// RecordRetrieval implements govern.Recorder.
func (l *AuditLog) RecordRetrieval(_ context.Context, entry govern.Entry) error {
	row := AuditRow{
		RecordedAt:     NowISO(),
		QueryID:        StableQueryID(entry.Query),
		Classification: entry.Classification,
		SourceFilters:  entry.SourceFilters,
		AllSources:     entry.AllSources,
		Agent:          entry.Agent,
		TaskID:         entry.TaskID,
		ResultCount:    entry.ResultCount,
		Embedder:       entry.Embedder,
		Model:          entry.Model,
	}
	line, err := json.Marshal(row)
	if err != nil {
		return fmt.Errorf("retrieval: cannot encode audit row: %w", err)
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	file, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("retrieval: cannot open audit log: %w", err)
	}
	defer func() { _ = file.Close() }()

	if _, err := file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("retrieval: cannot append audit row: %w", err)
	}
	// Synced before the retrieval is allowed to return. A row still in the
	// page cache when the process dies is a retrieval that happened and was
	// never recorded.
	if err := file.Sync(); err != nil {
		return fmt.Errorf("retrieval: cannot flush audit log: %w", err)
	}
	return nil
}
