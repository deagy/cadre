package orchestration

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Phase 3.1: Persistent Job Store using SQLite
// Manages dispatch job lifecycle with TTL-based expiry across process restarts

const (
	// Job status values
	JobStatusPending   = "pending"
	JobStatusRunning   = "running"
	JobStatusCompleted = "completed"
	JobStatusFailed    = "failed"
	JobStatusExpired   = "expired"
)

// PersistentDispatchJobStore manages job lifecycle with SQLite persistence
type PersistentDispatchJobStore struct {
	db *sql.DB
	mu sync.RWMutex
}

// DispatchJobRow represents a persisted job record
type DispatchJobRow struct {
	JobID      string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	Status     string
	ResultJSON string
}

// NewPersistentDispatchJobStore creates a new persistent job store
func NewPersistentDispatchJobStore(db *sql.DB) (*PersistentDispatchJobStore, error) {
	if db == nil {
		return nil, fmt.Errorf("database connection required")
	}

	store := &PersistentDispatchJobStore{db: db}

	// Create tables if they don't exist
	if err := store.createJobTables(); err != nil {
		return nil, fmt.Errorf("failed to create job tables: %w", err)
	}

	return store, nil
}

// createJobTables creates the job storage schema
func (pjs *PersistentDispatchJobStore) createJobTables() error {
	// Main job table
	const jobTableSchema = `
	CREATE TABLE IF NOT EXISTS dispatch_jobs (
		job_id TEXT PRIMARY KEY,
		created_at DATETIME NOT NULL,
		expires_at DATETIME NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending',
		result_json TEXT,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	`

	// Team job table
	const teamJobTableSchema = `
	CREATE TABLE IF NOT EXISTS dispatch_team_jobs (
		team_id TEXT PRIMARY KEY,
		created_at DATETIME NOT NULL,
		expires_at DATETIME NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending',
		result_json TEXT,
		member_count INTEGER DEFAULT 0,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);
	`

	// Create indexes for TTL-based queries
	const indexes = `
	CREATE INDEX IF NOT EXISTS idx_job_expires_at ON dispatch_jobs(expires_at);
	CREATE INDEX IF NOT EXISTS idx_job_status ON dispatch_jobs(status);
	CREATE INDEX IF NOT EXISTS idx_team_expires_at ON dispatch_team_jobs(expires_at);
	`

	if _, err := pjs.db.Exec(jobTableSchema); err != nil {
		return fmt.Errorf("failed to create dispatch_jobs table: %w", err)
	}

	if _, err := pjs.db.Exec(teamJobTableSchema); err != nil {
		return fmt.Errorf("failed to create dispatch_team_jobs table: %w", err)
	}

	if _, err := pjs.db.Exec(indexes); err != nil {
		return fmt.Errorf("failed to create indexes: %w", err)
	}

	return nil
}

// RecordJob stores a dispatch job result
func (pjs *PersistentDispatchJobStore) RecordJob(jobID string, result map[string]any) error {
	pjs.mu.Lock()
	defer pjs.mu.Unlock()

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal job result: %w", err)
	}

	now := time.Now().UTC()
	expiresAt := now.Add(DispatchJobTTLSeconds * time.Second)

	const query = `
	INSERT INTO dispatch_jobs (job_id, created_at, expires_at, status, result_json, updated_at)
	VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT(job_id) DO UPDATE SET
		status = excluded.status,
		result_json = excluded.result_json,
		updated_at = excluded.updated_at
	`

	_, err = pjs.db.Exec(query, jobID, now, expiresAt, JobStatusCompleted, resultJSON, now)
	if err != nil {
		return fmt.Errorf("failed to record job %q: %w", jobID, err)
	}

	return nil
}

// GetJob retrieves a job result, returning nil if not found or expired
func (pjs *PersistentDispatchJobStore) GetJob(jobID string) map[string]any {
	pjs.mu.RLock()
	defer pjs.mu.RUnlock()

	const query = `
	SELECT result_json, expires_at
	FROM dispatch_jobs
	WHERE job_id = ?
	`

	var resultJSON string
	var expiresAt time.Time

	err := pjs.db.QueryRow(query, jobID).Scan(&resultJSON, &expiresAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return nil
	}

	// Check TTL
	if time.Now().UTC().After(expiresAt) {
		// Job expired - clean it up in background
		go func() {
			_ = pjs.deleteExpiredJob(jobID)
		}()
		return nil
	}

	// Unmarshal result
	var result map[string]any
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		return nil
	}

	return result
}

// CleanupExpiredJobs removes jobs that have exceeded TTL
func (pjs *PersistentDispatchJobStore) CleanupExpiredJobs() (int, error) {
	pjs.mu.Lock()
	defer pjs.mu.Unlock()

	now := time.Now().UTC()

	const query = `DELETE FROM dispatch_jobs WHERE expires_at < ?`
	result, err := pjs.db.Exec(query, now)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup jobs: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}

	return int(affected), nil
}

// CleanupExpiredTeamJobs removes team jobs that have exceeded TTL
func (pjs *PersistentDispatchJobStore) CleanupExpiredTeamJobs() (int, error) {
	pjs.mu.Lock()
	defer pjs.mu.Unlock()

	now := time.Now().UTC()

	const query = `DELETE FROM dispatch_team_jobs WHERE expires_at < ?`
	result, err := pjs.db.Exec(query, now)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup team jobs: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}

	return int(affected), nil
}

// RecordTeamJob stores a team dispatch result
func (pjs *PersistentDispatchJobStore) RecordTeamJob(teamID string, result map[string]any) error {
	pjs.mu.Lock()
	defer pjs.mu.Unlock()

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal team result: %w", err)
	}

	now := time.Now().UTC()
	expiresAt := now.Add(DispatchJobTTLSeconds * time.Second)

	// Extract member count from result
	memberCount := 0
	if members, ok := result["members"].([]map[string]any); ok {
		memberCount = len(members)
	}

	const query = `
	INSERT INTO dispatch_team_jobs (team_id, created_at, expires_at, status, result_json, member_count, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(team_id) DO UPDATE SET
		status = excluded.status,
		result_json = excluded.result_json,
		member_count = excluded.member_count,
		updated_at = excluded.updated_at
	`

	_, err = pjs.db.Exec(query, teamID, now, expiresAt, JobStatusCompleted, resultJSON, memberCount, now)
	if err != nil {
		return fmt.Errorf("failed to record team job %q: %w", teamID, err)
	}

	return nil
}

// GetTeamJob retrieves a team job result, returning nil if not found or expired
func (pjs *PersistentDispatchJobStore) GetTeamJob(teamID string) map[string]any {
	pjs.mu.RLock()
	defer pjs.mu.RUnlock()

	const query = `
	SELECT result_json, expires_at
	FROM dispatch_team_jobs
	WHERE team_id = ?
	`

	var resultJSON string
	var expiresAt time.Time

	err := pjs.db.QueryRow(query, teamID).Scan(&resultJSON, &expiresAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return nil
	}

	// Check TTL
	if time.Now().UTC().After(expiresAt) {
		// Job expired - clean it up in background
		go func() {
			_ = pjs.deleteExpiredTeamJob(teamID)
		}()
		return nil
	}

	// Unmarshal result
	var result map[string]any
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		return nil
	}

	return result
}

// RecoverPendingJobs finds jobs older than timeout for recovery/cleanup
// Returns list of job IDs that should be handled
func (pjs *PersistentDispatchJobStore) RecoverPendingJobs() ([]string, error) {
	pjs.mu.RLock()
	defer pjs.mu.RUnlock()

	// Find jobs that were running but never completed, or jobs past MaxDispatchDepth timeout
	timeoutThreshold := time.Now().UTC().Add(-time.Duration(DefaultTimeoutSeconds) * time.Second)

	const query = `
	SELECT job_id
	FROM dispatch_jobs
	WHERE (status = ? OR status = ?)
	  AND created_at < ?
	  AND expires_at > ?
	ORDER BY created_at DESC
	`

	rows, err := pjs.db.Query(query, JobStatusPending, JobStatusRunning, timeoutThreshold, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("failed to query pending jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var jobIDs []string
	for rows.Next() {
		var jobID string
		if err := rows.Scan(&jobID); err != nil {
			continue
		}
		jobIDs = append(jobIDs, jobID)
	}

	return jobIDs, rows.Err()
}

// ListJobs returns all jobs, optionally filtered by status
func (pjs *PersistentDispatchJobStore) ListJobs(status string) ([]DispatchJobRow, error) {
	pjs.mu.RLock()
	defer pjs.mu.RUnlock()

	var query string
	var args []any

	if status == "" {
		query = `
		SELECT job_id, created_at, expires_at, status, result_json
		FROM dispatch_jobs
		WHERE expires_at > ?
		ORDER BY created_at DESC
		LIMIT 100
		`
		args = []any{time.Now().UTC()}
	} else {
		query = `
		SELECT job_id, created_at, expires_at, status, result_json
		FROM dispatch_jobs
		WHERE status = ? AND expires_at > ?
		ORDER BY created_at DESC
		LIMIT 100
		`
		args = []any{status, time.Now().UTC()}
	}

	rows, err := pjs.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var jobs []DispatchJobRow
	for rows.Next() {
		var job DispatchJobRow
		if err := rows.Scan(&job.JobID, &job.CreatedAt, &job.ExpiresAt, &job.Status, &job.ResultJSON); err != nil {
			continue
		}
		jobs = append(jobs, job)
	}

	return jobs, rows.Err()
}

// deleteExpiredJob removes a single expired job (internal, non-locking)
func (pjs *PersistentDispatchJobStore) deleteExpiredJob(jobID string) error {
	const query = `DELETE FROM dispatch_jobs WHERE job_id = ?`
	_, err := pjs.db.Exec(query, jobID)
	return err
}

// deleteExpiredTeamJob removes a single expired team job (internal, non-locking)
func (pjs *PersistentDispatchJobStore) deleteExpiredTeamJob(teamID string) error {
	const query = `DELETE FROM dispatch_team_jobs WHERE team_id = ?`
	_, err := pjs.db.Exec(query, teamID)
	return err
}

// Close closes the database connection
func (pjs *PersistentDispatchJobStore) Close() error {
	return pjs.db.Close()
}
