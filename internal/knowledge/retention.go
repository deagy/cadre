package knowledge

import (
	"fmt"
	"time"
)

// RetentionPolicy defines data retention rules.
type RetentionPolicy struct {
	Type          string        // "expiration", "classification", "source", "age"
	Reason        string        // Why deletion is occurring
	Classification *string      // For classification-based deletion
	Source        *string       // For source-based deletion
	MinAgeDays    int           // For age-based deletion
	AuthorizedBy  string        // Person/system authorizing deletion
}

// DeletionRun represents a deletion operation.
type DeletionRun struct {
	ID           string
	Reason       string
	PolicyType   string
	TargetCount  int64
	DeletedCount int64
	Status       string
	AuthorizedBy *string
	Classification *string
	Source       *string
	MinAgeDays   *int
	StartedAt    string
	CompletedAt  *string
	Error        *string
}

// ExpiredMessage represents a message that has passed its retention_until date.
type ExpiredMessage struct {
	ID            string
	Source        string
	ConversationID string
	Classification string
	RetentionUntil string
}

// GetExpiredMessages retrieves all messages past their retention_until date.
// Returns expired messages grouped by classification for audit purposes.
func (s *Store) GetExpiredMessages() ([]*ExpiredMessage, error) {
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	rows, err := s.db.Query(`
		SELECT id, source, conversation_id, classification, retention_until
		FROM messages
		WHERE retention_until IS NOT NULL AND retention_until < ?
		ORDER BY retention_until ASC
	`, now)

	if err != nil {
		return nil, fmt.Errorf("cannot query expired messages: %w", err)
	}
	defer rows.Close()

	var expired []*ExpiredMessage
	for rows.Next() {
		var msg ExpiredMessage
		if err := rows.Scan(
			&msg.ID, &msg.Source, &msg.ConversationID,
			&msg.Classification, &msg.RetentionUntil,
		); err != nil {
			return nil, fmt.Errorf("cannot scan expired message: %w", err)
		}
		expired = append(expired, &msg)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query error: %w", err)
	}

	return expired, nil
}

// DeleteExpired removes all expired messages and tracks deletion in a deletion_run.
// Returns the number of messages deleted and any error.
func (s *Store) DeleteExpired(authorizedBy string) (int64, error) {
	// Get expired messages
	expired, err := s.GetExpiredMessages()
	if err != nil {
		return 0, err
	}

	if len(expired) == 0 {
		return 0, nil
	}

	// Begin deletion run
	runID := newUUID()
	_, err = s.db.Exec(`
		INSERT INTO deletion_runs (
			id, reason, policy_type, target_count, deleted_count,
			status, authorized_by, started_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, runID, "retention_policy_expiration", "expiration",
		len(expired), 0, "running", authorizedBy, nowISO())

	if err != nil {
		return 0, fmt.Errorf("cannot begin deletion run: %w", err)
	}

	// Delete expired messages in transaction
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("cannot begin transaction: %w", err)
	}
	defer tx.Rollback()

	var deletedCount int64
	for _, msg := range expired {
		result, err := tx.Exec("DELETE FROM messages WHERE id = ?", msg.ID)
		if err != nil {
			s.recordDeletionRunError(runID, fmt.Sprintf("cannot delete message %s: %v", msg.ID, err))
			return 0, fmt.Errorf("cannot delete expired message: %w", err)
		}

		rows, err := result.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("cannot get rows affected: %w", err)
		}
		deletedCount += rows
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		s.recordDeletionRunError(runID, fmt.Sprintf("transaction commit failed: %v", err))
		return 0, fmt.Errorf("cannot commit deletion transaction: %w", err)
	}

	// Mark deletion run complete
	_, err = s.db.Exec(`
		UPDATE deletion_runs
		SET status = 'complete', deleted_count = ?, completed_at = ?
		WHERE id = ?
	`, deletedCount, nowISO(), runID)

	if err != nil {
		return deletedCount, fmt.Errorf("cannot complete deletion run: %w", err)
	}

	return deletedCount, nil
}

// DeleteByClassification removes all messages with a given classification.
// Used for purging entire classification levels.
func (s *Store) DeleteByClassification(classification, reason, authorizedBy string) (int64, error) {
	if classification == "" {
		return 0, fmt.Errorf("classification is required")
	}

	// Count target messages
	var targetCount int64
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM messages WHERE classification = ?", classification,
	).Scan(&targetCount)
	if err != nil {
		return 0, fmt.Errorf("cannot count messages: %w", err)
	}

	if targetCount == 0 {
		return 0, nil
	}

	// Begin deletion run
	runID := newUUID()
	_, err = s.db.Exec(`
		INSERT INTO deletion_runs (
			id, reason, policy_type, target_count, deleted_count,
			status, authorized_by, classification, started_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, runID, reason, "classification", targetCount, 0,
		"running", authorizedBy, classification, nowISO())

	if err != nil {
		return 0, fmt.Errorf("cannot begin deletion run: %w", err)
	}

	// Delete messages in transaction
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("cannot begin transaction: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.Exec("DELETE FROM messages WHERE classification = ?", classification)
	if err != nil {
		s.recordDeletionRunError(runID, fmt.Sprintf("cannot delete by classification: %v", err))
		return 0, fmt.Errorf("cannot delete messages: %w", err)
	}

	deletedCount, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("cannot get rows affected: %w", err)
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		s.recordDeletionRunError(runID, fmt.Sprintf("transaction commit failed: %v", err))
		return 0, fmt.Errorf("cannot commit deletion transaction: %w", err)
	}

	// Mark deletion run complete
	_, err = s.db.Exec(`
		UPDATE deletion_runs
		SET status = 'complete', deleted_count = ?, completed_at = ?
		WHERE id = ?
	`, deletedCount, nowISO(), runID)

	if err != nil {
		return deletedCount, fmt.Errorf("cannot complete deletion run: %w", err)
	}

	return deletedCount, nil
}

// DeleteBySource removes all messages from a given source.
// Used for source-level data purging.
func (s *Store) DeleteBySource(source, reason, authorizedBy string) (int64, error) {
	if source == "" {
		return 0, fmt.Errorf("source is required")
	}

	// Count target messages
	var targetCount int64
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM messages WHERE source = ?", source,
	).Scan(&targetCount)
	if err != nil {
		return 0, fmt.Errorf("cannot count messages: %w", err)
	}

	if targetCount == 0 {
		return 0, nil
	}

	// Begin deletion run
	runID := newUUID()
	_, err = s.db.Exec(`
		INSERT INTO deletion_runs (
			id, reason, policy_type, target_count, deleted_count,
			status, authorized_by, source, started_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, runID, reason, "source", targetCount, 0,
		"running", authorizedBy, source, nowISO())

	if err != nil {
		return 0, fmt.Errorf("cannot begin deletion run: %w", err)
	}

	// Delete messages in transaction
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("cannot begin transaction: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.Exec("DELETE FROM messages WHERE source = ?", source)
	if err != nil {
		s.recordDeletionRunError(runID, fmt.Sprintf("cannot delete by source: %v", err))
		return 0, fmt.Errorf("cannot delete messages: %w", err)
	}

	deletedCount, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("cannot get rows affected: %w", err)
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		s.recordDeletionRunError(runID, fmt.Sprintf("transaction commit failed: %v", err))
		return 0, fmt.Errorf("cannot commit deletion transaction: %w", err)
	}

	// Mark deletion run complete
	_, err = s.db.Exec(`
		UPDATE deletion_runs
		SET status = 'complete', deleted_count = ?, completed_at = ?
		WHERE id = ?
	`, deletedCount, nowISO(), runID)

	if err != nil {
		return deletedCount, fmt.Errorf("cannot complete deletion run: %w", err)
	}

	return deletedCount, nil
}

// DeleteByAge removes messages older than a specified number of days.
// Used for periodic cleanup of old data.
func (s *Store) DeleteByAge(minAgeDays int, classification *string, reason, authorizedBy string) (int64, error) {
	if minAgeDays < 0 {
		return 0, fmt.Errorf("minAgeDays must be non-negative")
	}

	// Calculate cutoff date (now minus minAgeDays)
	cutoffTime := time.Now().UTC().AddDate(0, 0, -minAgeDays)
	cutoffISO := cutoffTime.Format("2006-01-02T15:04:05.000Z")

	// Build query with optional classification filter
	countQuery := "SELECT COUNT(*) FROM messages WHERE ingested_at < ?"
	deleteQuery := "DELETE FROM messages WHERE ingested_at < ?"
	args := []interface{}{cutoffISO}

	if classification != nil {
		countQuery += " AND classification = ?"
		deleteQuery += " AND classification = ?"
		args = append(args, *classification)
	}

	// Count target messages
	var targetCount int64
	err := s.db.QueryRow(countQuery, args...).Scan(&targetCount)
	if err != nil {
		return 0, fmt.Errorf("cannot count messages: %w", err)
	}

	if targetCount == 0 {
		return 0, nil
	}

	// Begin deletion run
	runID := newUUID()
	_, err = s.db.Exec(`
		INSERT INTO deletion_runs (
			id, reason, policy_type, target_count, deleted_count,
			status, authorized_by, classification, min_age_days, started_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, runID, reason, "age", targetCount, 0,
		"running", authorizedBy, classification, minAgeDays, nowISO())

	if err != nil {
		return 0, fmt.Errorf("cannot begin deletion run: %w", err)
	}

	// Delete messages in transaction
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("cannot begin transaction: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.Exec(deleteQuery, args...)
	if err != nil {
		s.recordDeletionRunError(runID, fmt.Sprintf("cannot delete by age: %v", err))
		return 0, fmt.Errorf("cannot delete messages: %w", err)
	}

	deletedCount, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("cannot get rows affected: %w", err)
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		s.recordDeletionRunError(runID, fmt.Sprintf("transaction commit failed: %v", err))
		return 0, fmt.Errorf("cannot commit deletion transaction: %w", err)
	}

	// Mark deletion run complete
	_, err = s.db.Exec(`
		UPDATE deletion_runs
		SET status = 'complete', deleted_count = ?, completed_at = ?
		WHERE id = ?
	`, deletedCount, nowISO(), runID)

	if err != nil {
		return deletedCount, fmt.Errorf("cannot complete deletion run: %w", err)
	}

	return deletedCount, nil
}

// GetDeletionHistory retrieves all deletion runs, optionally filtered by status.
// Returns deletion runs in reverse chronological order.
func (s *Store) GetDeletionHistory(statusFilter *string) ([]*DeletionRun, error) {
	query := `
		SELECT id, reason, policy_type, target_count, deleted_count,
		       status, authorized_by, classification, source, min_age_days,
		       started_at, completed_at, error
		FROM deletion_runs
	`
	args := []interface{}{}

	if statusFilter != nil {
		query += "WHERE status = ?"
		args = append(args, *statusFilter)
	}

	query += " ORDER BY started_at DESC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("cannot query deletion history: %w", err)
	}
	defer rows.Close()

	var deletions []*DeletionRun
	for rows.Next() {
		var run DeletionRun
		if err := rows.Scan(
			&run.ID, &run.Reason, &run.PolicyType, &run.TargetCount, &run.DeletedCount,
			&run.Status, &run.AuthorizedBy, &run.Classification, &run.Source,
			&run.MinAgeDays, &run.StartedAt, &run.CompletedAt, &run.Error,
		); err != nil {
			return nil, fmt.Errorf("cannot scan deletion run: %w", err)
		}
		deletions = append(deletions, &run)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query error: %w", err)
	}

	return deletions, nil
}

// GetDeletionStats returns statistics about deletion operations.
func (s *Store) GetDeletionStats() (map[string]int64, error) {
	stats := make(map[string]int64)

	rows, err := s.db.Query(`
		SELECT policy_type, SUM(deleted_count) as total_deleted, COUNT(*) as run_count
		FROM deletion_runs
		WHERE status = 'complete'
		GROUP BY policy_type
	`)

	if err != nil {
		return nil, fmt.Errorf("cannot query deletion stats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var policyType string
		var totalDeleted, runCount int64
		if err := rows.Scan(&policyType, &totalDeleted, &runCount); err != nil {
			return nil, fmt.Errorf("cannot scan deletion stat: %w", err)
		}
		stats[fmt.Sprintf("%s_deleted", policyType)] = totalDeleted
		stats[fmt.Sprintf("%s_runs", policyType)] = runCount
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query error: %w", err)
	}

	return stats, nil
}

// helper function to record errors in deletion runs
func (s *Store) recordDeletionRunError(runID string, errMsg string) {
	if len(errMsg) > 2000 {
		errMsg = errMsg[:2000]
	}
	_, _ = s.db.Exec(`
		UPDATE deletion_runs
		SET status = 'failed', error = ?, completed_at = ?
		WHERE id = ?
	`, errMsg, nowISO(), runID)
}
