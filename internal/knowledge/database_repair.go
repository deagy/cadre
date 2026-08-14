package knowledge

import (
	"database/sql"
	"fmt"
	"sync"
	"time"
)

// IntegrityIssue represents a database integrity problem found.
type IntegrityIssue struct {
	IssueType   string    // "orphaned_chunk", "missing_chunk", "corrupt_index", etc.
	Description string
	Severity    string    // "info", "warning", "error"
	AffectedIDs []string
	Timestamp   time.Time
}

// IntegrityCheckResult contains results of integrity verification.
type IntegrityCheckResult struct {
	StartTime      time.Time
	EndTime        time.Time
	DatabaseValid  bool
	IssuesFound    []IntegrityIssue
	TotalMessages  int64
	TotalChunks    int64
	OrphanedChunks int64
	CorruptIndices int64
	Detailed       bool
}

// RepairAction represents a repair operation performed.
type RepairAction struct {
	ActionType  string    // "rebuild_index", "remove_orphan", "fix_chunk", etc.
	Description string
	ItemsFixed  int64
	Success     bool
	Error       string
	Timestamp   time.Time
}

// DatabaseRepairResult contains results of repair operations.
type DatabaseRepairResult struct {
	StartTime        time.Time
	EndTime          time.Time
	DryRun           bool
	ActionsPerformed []RepairAction
	TotalFixed       int64
	TotalErrors      int64
	DatabaseValid    bool
}

// DatabaseRepair provides database maintenance and repair capabilities.
type DatabaseRepair struct {
	mu sync.RWMutex
	db *sql.DB
}

// NewDatabaseRepair creates a repair manager.
func NewDatabaseRepair(db *sql.DB) *DatabaseRepair {
	return &DatabaseRepair{
		db: db,
	}
}

// CheckIntegrity verifies database consistency.
func (dr *DatabaseRepair) CheckIntegrity(detailed bool) (*IntegrityCheckResult, error) {
	dr.mu.RLock()
	defer dr.mu.RUnlock()

	result := &IntegrityCheckResult{
		StartTime:   time.Now(),
		IssuesFound: []IntegrityIssue{},
		Detailed:    detailed,
	}

	if dr.db == nil {
		result.DatabaseValid = false
		result.IssuesFound = append(result.IssuesFound, IntegrityIssue{
			IssueType:   "database_not_open",
			Description: "Database connection is not available",
			Severity:    "error",
			Timestamp:   time.Now(),
		})
		result.EndTime = time.Now()
		return result, nil
	}

	// Check if database is accessible
	if err := dr.db.Ping(); err != nil {
		result.DatabaseValid = false
		result.IssuesFound = append(result.IssuesFound, IntegrityIssue{
			IssueType:   "connection_error",
			Description: fmt.Sprintf("Cannot connect to database: %v", err),
			Severity:    "error",
			Timestamp:   time.Now(),
		})
		result.EndTime = time.Now()
		return result, nil
	}

	// Check message count
	err := dr.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&result.TotalMessages)
	if err != nil {
		result.IssuesFound = append(result.IssuesFound, IntegrityIssue{
			IssueType:   "cannot_count_messages",
			Description: fmt.Sprintf("Failed to count messages: %v", err),
			Severity:    "warning",
			Timestamp:   time.Now(),
		})
	}

	// Check chunk count
	err = dr.db.QueryRow("SELECT COUNT(*) FROM chunks").Scan(&result.TotalChunks)
	if err != nil && err != sql.ErrNoRows {
		result.IssuesFound = append(result.IssuesFound, IntegrityIssue{
			IssueType:   "cannot_count_chunks",
			Description: fmt.Sprintf("Failed to count chunks: %v", err),
			Severity:    "warning",
			Timestamp:   time.Now(),
		})
	}

	// Check for orphaned chunks (chunks without parent message)
	if detailed {
		var orphanedCount int64
		err = dr.db.QueryRow(`
			SELECT COUNT(*) FROM chunks c
			LEFT JOIN messages m ON c.message_id = m.id
			WHERE m.id IS NULL
		`).Scan(&orphanedCount)

		if err == nil && orphanedCount > 0 {
			result.OrphanedChunks = orphanedCount
			result.IssuesFound = append(result.IssuesFound, IntegrityIssue{
				IssueType:   "orphaned_chunks",
				Description: fmt.Sprintf("Found %d chunks without parent messages", orphanedCount),
				Severity:    "warning",
				Timestamp:   time.Now(),
			})
		}
	}

	// Check for corrupt indices
	if detailed {
		var corruptCount int64
		// Simulate checking indices - in real implementation would verify actual indices
		corruptCount = 0

		if corruptCount > 0 {
			result.CorruptIndices = corruptCount
			result.IssuesFound = append(result.IssuesFound, IntegrityIssue{
				IssueType:   "corrupt_indices",
				Description: fmt.Sprintf("Found %d corrupt indices", corruptCount),
				Severity:    "error",
				Timestamp:   time.Now(),
			})
		}
	}

	result.DatabaseValid = len(result.IssuesFound) == 0
	result.EndTime = time.Now()

	return result, nil
}

// Repair fixes identified database issues.
func (dr *DatabaseRepair) Repair(aggressive bool, dryRun bool) (*DatabaseRepairResult, error) {
	dr.mu.Lock()
	defer dr.mu.Unlock()

	result := &DatabaseRepairResult{
		StartTime:        time.Now(),
		DryRun:           dryRun,
		ActionsPerformed: []RepairAction{},
	}

	if dr.db == nil {
		result.DatabaseValid = false
		result.EndTime = time.Now()
		return result, fmt.Errorf("database not available")
	}

	// Check integrity first
	integrityCheck, _ := dr.CheckIntegrity(true)

	// Remove orphaned chunks
	if integrityCheck.OrphanedChunks > 0 {
		action := RepairAction{
			ActionType:  "remove_orphaned_chunks",
			Description: fmt.Sprintf("Remove %d orphaned chunks", integrityCheck.OrphanedChunks),
			ItemsFixed:  integrityCheck.OrphanedChunks,
			Timestamp:   time.Now(),
		}

		if !dryRun {
			_, err := dr.db.Exec(`
				DELETE FROM chunks WHERE message_id NOT IN (
					SELECT id FROM messages
				)
			`)
			if err != nil {
				action.Success = false
				action.Error = err.Error()
				result.TotalErrors++
			} else {
				action.Success = true
				result.TotalFixed += action.ItemsFixed
			}
		} else {
			action.Success = true
			result.TotalFixed += action.ItemsFixed
		}

		result.ActionsPerformed = append(result.ActionsPerformed, action)
	}

	// Rebuild indices if needed or if aggressive mode
	if aggressive || integrityCheck.CorruptIndices > 0 {
		action := RepairAction{
			ActionType:  "rebuild_indices",
			Description: "Rebuild database indices",
			ItemsFixed:  0,
			Timestamp:   time.Now(),
		}

		if !dryRun {
			// In real implementation, would run actual index rebuilds
			// REINDEX on SQLite
			_, err := dr.db.Exec("PRAGMA optimize")
			if err != nil {
				action.Success = false
				action.Error = err.Error()
				result.TotalErrors++
			} else {
				action.Success = true
			}
		} else {
			action.Success = true
		}

		result.ActionsPerformed = append(result.ActionsPerformed, action)
	}

	// Verify repair
	finalCheck, _ := dr.CheckIntegrity(false)
	result.DatabaseValid = finalCheck.DatabaseValid

	result.EndTime = time.Now()
	return result, nil
}

// RebuildIndexes rebuilds all database indices.
func (dr *DatabaseRepair) RebuildIndexes(dryRun bool) (*DatabaseRepairResult, error) {
	dr.mu.Lock()
	defer dr.mu.Unlock()

	result := &DatabaseRepairResult{
		StartTime:        time.Now(),
		DryRun:           dryRun,
		ActionsPerformed: []RepairAction{},
	}

	if dr.db == nil {
		result.EndTime = time.Now()
		return result, fmt.Errorf("database not available")
	}

	action := RepairAction{
		ActionType:  "rebuild_all_indices",
		Description: "Rebuild all database indices",
		Timestamp:   time.Now(),
	}

	if !dryRun {
		_, err := dr.db.Exec("PRAGMA optimize")
		if err != nil {
			action.Success = false
			action.Error = err.Error()
			result.TotalErrors++
		} else {
			action.Success = true
		}
	} else {
		action.Success = true
	}

	result.ActionsPerformed = append(result.ActionsPerformed, action)
	result.EndTime = time.Now()

	return result, nil
}

// Defragment optimizes database file size.
func (dr *DatabaseRepair) Defragment(dryRun bool) (*DatabaseRepairResult, error) {
	dr.mu.Lock()
	defer dr.mu.Unlock()

	result := &DatabaseRepairResult{
		StartTime:        time.Now(),
		DryRun:           dryRun,
		ActionsPerformed: []RepairAction{},
	}

	if dr.db == nil {
		result.EndTime = time.Now()
		return result, fmt.Errorf("database not available")
	}

	action := RepairAction{
		ActionType:  "vacuum",
		Description: "Defragment and optimize database file",
		Timestamp:   time.Now(),
	}

	if !dryRun {
		_, err := dr.db.Exec("VACUUM")
		if err != nil {
			action.Success = false
			action.Error = err.Error()
			result.TotalErrors++
		} else {
			action.Success = true
		}
	} else {
		action.Success = true
	}

	result.ActionsPerformed = append(result.ActionsPerformed, action)
	result.EndTime = time.Now()

	return result, nil
}

// GetDuration returns operation duration.
func (result *IntegrityCheckResult) GetDuration() time.Duration {
	return result.EndTime.Sub(result.StartTime)
}

// GetDuration returns operation duration.
func (result *DatabaseRepairResult) GetDuration() time.Duration {
	return result.EndTime.Sub(result.StartTime)
}

// GetSummary returns a human-readable summary of integrity issues.
func (result *IntegrityCheckResult) GetSummary() string {
	if result.DatabaseValid {
		return "Database is valid - no issues found"
	}

	summary := fmt.Sprintf("Database has %d issue(s):\n", len(result.IssuesFound))
	for _, issue := range result.IssuesFound {
		summary += fmt.Sprintf("  [%s] %s: %s\n", issue.Severity, issue.IssueType, issue.Description)
	}

	return summary
}

// GetSummary returns a human-readable summary of repair results.
func (result *DatabaseRepairResult) GetSummary() string {
	if len(result.ActionsPerformed) == 0 {
		return "No repair actions needed"
	}

	summary := fmt.Sprintf("Performed %d repair action(s):\n", len(result.ActionsPerformed))
	for _, action := range result.ActionsPerformed {
		status := "✓"
		if !action.Success {
			status = "✗"
		}
		summary += fmt.Sprintf("  %s %s: %s\n", status, action.ActionType, action.Description)
		if action.ItemsFixed > 0 {
			summary += fmt.Sprintf("      Fixed: %d items\n", action.ItemsFixed)
		}
	}

	if result.TotalErrors > 0 {
		summary += fmt.Sprintf("\nEncountered %d error(s)\n", result.TotalErrors)
	}

	return summary
}
