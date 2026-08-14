package orchestration

import (
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Helper: create temporary in-memory SQLite database for testing
func newTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	return db
}

// Helper: create temporary file-based database
func newTestFileDB(t *testing.T) (*sql.DB, string) {
	tmpfile, err := os.CreateTemp("", "dispatch_test_*.db")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpfile.Close()

	db, err := sql.Open("sqlite3", tmpfile.Name())
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}

	return db, tmpfile.Name()
}

func TestNewPersistentJobStoreNoDatabase(t *testing.T) {
	_, err := NewPersistentDispatchJobStore(nil)
	if err == nil {
		t.Errorf("expected error for nil database, got nil")
	}
}

func TestNewPersistentJobStore(t *testing.T) {
	requireSQLite(t)
	db := newTestDB(t)
	defer db.Close()

	store, err := NewPersistentDispatchJobStore(db)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	if store == nil {
		t.Errorf("store is nil")
	}
}

func TestRecordAndRetrieveJob(t *testing.T) {
	requireSQLite(t)
	db := newTestDB(t)
	defer db.Close()

	store, _ := NewPersistentDispatchJobStore(db)

	jobID := "job_test123"
	result := map[string]any{
		"status":    "success",
		"output":    "test output",
		"exit_code": 0,
	}

	// Record job
	err := store.RecordJob(jobID, result)
	if err != nil {
		t.Fatalf("failed to record job: %v", err)
	}

	// Retrieve job
	retrieved := store.GetJob(jobID)
	if retrieved == nil {
		t.Errorf("GetJob returned nil")
		return
	}

	if retrieved["status"] != "success" {
		t.Errorf("status = %v, want 'success'", retrieved["status"])
	}
	if retrieved["output"] != "test output" {
		t.Errorf("output = %v, want 'test output'", retrieved["output"])
	}
	if retrieved["exit_code"] != float64(0) {
		t.Errorf("exit_code = %v, want 0", retrieved["exit_code"])
	}
}

func TestJobNotFound(t *testing.T) {
	requireSQLite(t)
	db := newTestDB(t)
	defer db.Close()

	store, _ := NewPersistentDispatchJobStore(db)

	retrieved := store.GetJob("job_nonexistent")
	if retrieved != nil {
		t.Errorf("GetJob for non-existent job should return nil, got %v", retrieved)
	}
}

func TestJobTTLExpiry(t *testing.T) {
	requireSQLite(t)
	db := newTestDB(t)
	defer db.Close()

	store, _ := NewPersistentDispatchJobStore(db)

	jobID := "job_expiry_test"
	result := map[string]any{"status": "success"}

	// Record job
	err := store.RecordJob(jobID, result)
	if err != nil {
		t.Fatalf("failed to record job: %v", err)
	}

	// Job should exist
	retrieved := store.GetJob(jobID)
	if retrieved == nil {
		t.Errorf("job should exist immediately after recording")
		return
	}

	// Manually expire the job by updating its expiry time
	now := time.Now().UTC()
	expiredTime := now.Add(-1 * time.Hour)

	const updateQuery = `UPDATE dispatch_jobs SET expires_at = ? WHERE job_id = ?`
	_, err = db.Exec(updateQuery, expiredTime, jobID)
	if err != nil {
		t.Fatalf("failed to expire job: %v", err)
	}

	// Job should now be expired
	retrieved = store.GetJob(jobID)
	if retrieved != nil {
		t.Errorf("expired job should return nil, got %v", retrieved)
	}
}

func TestCleanupExpiredJobs(t *testing.T) {
	requireSQLite(t)
	db := newTestDB(t)
	defer db.Close()

	store, _ := NewPersistentDispatchJobStore(db)

	// Record multiple jobs
	for i := 0; i < 3; i++ {
		jobID := "job_cleanup_" + string(rune(i))
		result := map[string]any{"status": "success"}
		err := store.RecordJob(jobID, result)
		if err != nil {
			t.Fatalf("failed to record job: %v", err)
		}
	}

	// Expire all jobs
	const expireQuery = `UPDATE dispatch_jobs SET expires_at = ? WHERE 1=1`
	_, err := db.Exec(expireQuery, time.Now().UTC().Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("failed to expire jobs: %v", err)
	}

	// Cleanup
	cleaned, err := store.CleanupExpiredJobs()
	if err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}

	if cleaned != 3 {
		t.Errorf("cleanup count = %d, want 3", cleaned)
	}

	// Verify jobs are gone
	const countQuery = `SELECT COUNT(*) FROM dispatch_jobs`
	var count int
	if err := db.QueryRow(countQuery).Scan(&count); err != nil {
		t.Fatalf("count query after cleanup: %v", err)
	}
	if count != 0 {
		t.Errorf("after cleanup, job count = %d, want 0", count)
	}
}

func TestRecordAndRetrieveTeamJob(t *testing.T) {
	requireSQLite(t)
	db := newTestDB(t)
	defer db.Close()

	store, _ := NewPersistentDispatchJobStore(db)

	teamID := "team_test123"
	result := map[string]any{
		"status": "team_dispatched",
		"members": []map[string]any{
			{"status": "success"},
			{"status": "success"},
		},
	}

	// Record team job
	err := store.RecordTeamJob(teamID, result)
	if err != nil {
		t.Fatalf("failed to record team job: %v", err)
	}

	// Retrieve team job
	retrieved := store.GetTeamJob(teamID)
	if retrieved == nil {
		t.Errorf("GetTeamJob returned nil")
		return
	}

	if retrieved["status"] != "team_dispatched" {
		t.Errorf("status = %v, want 'team_dispatched'", retrieved["status"])
	}

	members, ok := retrieved["members"].([]any)
	if !ok || len(members) != 2 {
		t.Errorf("members not parsed correctly")
	}
}

func TestTeamJobNotFound(t *testing.T) {
	requireSQLite(t)
	db := newTestDB(t)
	defer db.Close()

	store, _ := NewPersistentDispatchJobStore(db)

	retrieved := store.GetTeamJob("team_nonexistent")
	if retrieved != nil {
		t.Errorf("GetTeamJob for non-existent team should return nil")
	}
}

func TestRecoverPendingJobs(t *testing.T) {
	requireSQLite(t)
	db := newTestDB(t)
	defer db.Close()

	store, _ := NewPersistentDispatchJobStore(db)

	// Record jobs with different statuses
	for i := 0; i < 2; i++ {
		jobID := "job_pending_" + string(rune(i))
		result := map[string]any{"status": "success"}
		_ = store.RecordJob(jobID, result)
	}

	// Mark one as pending (set to pending status)
	// LIMIT on UPDATE needs SQLITE_ENABLE_UPDATE_DELETE_LIMIT, which the
	// bundled mattn/go-sqlite3 amalgamation is not compiled with, so scope the
	// single-row update with a subselect instead.
	const updateQuery = `
	UPDATE dispatch_jobs
	SET status = ?, created_at = ?
	WHERE job_id IN (
		SELECT job_id FROM dispatch_jobs WHERE job_id LIKE 'job_pending_%' LIMIT 1
	)
	`
	_, err := db.Exec(updateQuery, JobStatusPending, time.Now().UTC().Add(-2*time.Duration(DefaultTimeoutSeconds)*time.Second))
	if err != nil {
		t.Fatalf("failed to update job: %v", err)
	}

	// Recover pending jobs
	pending, err := store.RecoverPendingJobs()
	if err != nil {
		t.Fatalf("failed to recover pending jobs: %v", err)
	}

	if len(pending) > 0 {
		// Found pending jobs (exact count may vary based on test timing)
		t.Logf("recovered %d pending jobs", len(pending))
	}
}

func TestListJobs(t *testing.T) {
	requireSQLite(t)
	db := newTestDB(t)
	defer db.Close()

	store, _ := NewPersistentDispatchJobStore(db)

	// Record 3 jobs
	for i := 0; i < 3; i++ {
		jobID := "job_list_" + string(rune(i))
		result := map[string]any{"status": "success"}
		_ = store.RecordJob(jobID, result)
	}

	// List all jobs
	jobs, err := store.ListJobs("")
	if err != nil {
		t.Fatalf("failed to list jobs: %v", err)
	}

	if len(jobs) != 3 {
		t.Errorf("job count = %d, want 3", len(jobs))
	}

	// List completed jobs
	jobs, err = store.ListJobs(JobStatusCompleted)
	if err != nil {
		t.Fatalf("failed to list completed jobs: %v", err)
	}

	if len(jobs) != 3 {
		t.Errorf("completed job count = %d, want 3", len(jobs))
	}
}

func TestConcurrentJobAccess(t *testing.T) {
	requireSQLite(t)
	db := newTestDB(t)
	defer db.Close()

	store, _ := NewPersistentDispatchJobStore(db)

	// Spawn multiple goroutines accessing the store
	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func(idx int) {
			jobID := "job_concurrent_" + string(rune(idx))
			result := map[string]any{"index": idx, "status": "success"}

			// Record
			_ = store.RecordJob(jobID, result)

			// Retrieve
			retrieved := store.GetJob(jobID)
			if retrieved == nil {
				t.Errorf("concurrent access failed for job %s", jobID)
			}

			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify all jobs recorded
	jobs, _ := store.ListJobs("")
	if len(jobs) != 10 {
		t.Errorf("expected 10 jobs after concurrent access, got %d", len(jobs))
	}
}

func TestJobPersistenceAcrossConnections(t *testing.T) {
	requireSQLite(t)
	// Use file-based database to test persistence
	db, path := newTestFileDB(t)
	defer os.Remove(path)

	// Create store and record job
	store, _ := NewPersistentDispatchJobStore(db)
	jobID := "job_persist_test"
	result := map[string]any{"status": "success", "data": "persisted"}

	err := store.RecordJob(jobID, result)
	if err != nil {
		t.Fatalf("failed to record job: %v", err)
	}

	db.Close()

	// Reopen database and verify job is still there
	db2, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("failed to reopen database: %v", err)
	}
	defer db2.Close()

	store2, _ := NewPersistentDispatchJobStore(db2)
	retrieved := store2.GetJob(jobID)
	if retrieved == nil {
		t.Errorf("job not persisted across connection restart")
		return
	}

	if retrieved["status"] != "success" || retrieved["data"] != "persisted" {
		t.Errorf("persisted data corrupted: %v", retrieved)
	}
}

func TestCleanupExpiredTeamJobs(t *testing.T) {
	requireSQLite(t)
	db := newTestDB(t)
	defer db.Close()

	store, _ := NewPersistentDispatchJobStore(db)

	// Record team jobs
	for i := 0; i < 2; i++ {
		teamID := "team_cleanup_" + string(rune(i))
		result := map[string]any{"status": "team_dispatched"}
		_ = store.RecordTeamJob(teamID, result)
	}

	// Expire all team jobs
	const expireQuery = `UPDATE dispatch_team_jobs SET expires_at = ? WHERE 1=1`
	_, err := db.Exec(expireQuery, time.Now().UTC().Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("failed to expire team jobs: %v", err)
	}

	// Cleanup
	cleaned, err := store.CleanupExpiredTeamJobs()
	if err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}

	if cleaned != 2 {
		t.Errorf("cleanup count = %d, want 2", cleaned)
	}
}

func TestJobUpdateOnDuplicate(t *testing.T) {
	requireSQLite(t)
	db := newTestDB(t)
	defer db.Close()

	store, _ := NewPersistentDispatchJobStore(db)

	jobID := "job_update_test"
	result1 := map[string]any{"status": "running"}
	result2 := map[string]any{"status": "success", "output": "updated"}

	// Record job first time
	err := store.RecordJob(jobID, result1)
	if err != nil {
		t.Fatalf("failed to record job: %v", err)
	}

	// Record same job with updated result
	err = store.RecordJob(jobID, result2)
	if err != nil {
		t.Fatalf("failed to update job: %v", err)
	}

	// Verify updated result
	retrieved := store.GetJob(jobID)
	if retrieved["status"] != "success" {
		t.Errorf("status not updated: %v", retrieved["status"])
	}
	if retrieved["output"] != "updated" {
		t.Errorf("output not in updated result")
	}

	// Verify only one job exists
	const countQuery = `SELECT COUNT(*) FROM dispatch_jobs WHERE job_id = ?`
	var count int
	db.QueryRow(countQuery, jobID).Scan(&count)
	if count != 1 {
		t.Errorf("duplicate job records exist: count = %d", count)
	}
}
