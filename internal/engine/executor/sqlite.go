package executor

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// BusyTimeout is how long a connection waits for a lock before giving up.
//
// A run is advanced by whatever process holds the human's answer -- a CLI
// invocation, a service handler -- so two processes touching the same store at
// once is ordinary rather than exceptional.
const BusyTimeout = 5 * time.Second

// SQLiteCheckpointer persists runs to a SQLite file.
type SQLiteCheckpointer struct {
	db *sql.DB
}

// OpenSQLiteCheckpointer opens (and creates) a checkpoint store.
//
// Pragmas are in the DSN rather than run afterwards through Exec. database/sql
// hands out pooled connections, so a PRAGMA issued via Exec applies to
// whichever connection served it and to no other -- a busy_timeout set that
// way is simply absent on the next connection. internal/knowledge learned that
// as "database is locked" under concurrent use; this store opens the same way
// so it cannot relearn it.
func OpenSQLiteCheckpointer(path string) (*SQLiteCheckpointer, error) {
	db, err := sql.Open("sqlite3", fmt.Sprintf(
		"file:%s?_busy_timeout=%d&_journal_mode=WAL&_foreign_keys=on",
		path, BusyTimeout.Milliseconds()))
	if err != nil {
		return nil, fmt.Errorf("cannot open the checkpoint store: %w", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS run_checkpoints (
			task_id     TEXT PRIMARY KEY,
			checkpoint  TEXT NOT NULL,
			updated_at  TEXT NOT NULL
		)`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("cannot create the checkpoint table: %w", err)
	}
	return &SQLiteCheckpointer{db: db}, nil
}

// Close releases the store.
func (s *SQLiteCheckpointer) Close() error { return s.db.Close() }

// Load returns a run's checkpoint.
func (s *SQLiteCheckpointer) Load(taskID string) (Checkpoint, bool, error) {
	var encoded string
	err := s.db.QueryRow(`SELECT checkpoint FROM run_checkpoints WHERE task_id = ?`, taskID).Scan(&encoded)
	if err == sql.ErrNoRows {
		return Checkpoint{}, false, nil
	}
	if err != nil {
		return Checkpoint{}, false, err
	}
	var checkpoint Checkpoint
	if err := json.Unmarshal([]byte(encoded), &checkpoint); err != nil {
		return Checkpoint{}, false, fmt.Errorf("checkpoint for task %s is unreadable: %w", taskID, err)
	}
	return checkpoint, true, nil
}

// Save writes a run's checkpoint, replacing any previous one.
//
// One row per task: a run has exactly one current position, and keeping older
// ones would invite resuming from a stale point.
func (s *SQLiteCheckpointer) Save(taskID string, checkpoint Checkpoint) error {
	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		INSERT INTO run_checkpoints (task_id, checkpoint, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(task_id) DO UPDATE SET checkpoint = excluded.checkpoint, updated_at = excluded.updated_at`,
		taskID, string(encoded), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// Rewind forgets a run's position.
func (s *SQLiteCheckpointer) Rewind(taskID string) error {
	_, err := s.db.Exec(`DELETE FROM run_checkpoints WHERE task_id = ?`, taskID)
	return err
}
