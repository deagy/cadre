package knowledge

import (
	"testing"
)

func TestNewDatabaseRepair(t *testing.T) {
	repair := NewDatabaseRepair(nil)
	if repair == nil {
		t.Error("NewDatabaseRepair should not return nil")
	}
}

func TestCheckIntegrityNilDatabase(t *testing.T) {
	repair := NewDatabaseRepair(nil)

	result, _ := repair.CheckIntegrity(false)

	if result.DatabaseValid {
		t.Error("Expected database to be invalid when connection is nil")
	}

	if len(result.IssuesFound) == 0 {
		t.Error("Expected issues to be found")
	}

	if result.IssuesFound[0].IssueType != "database_not_open" {
		t.Errorf("Expected 'database_not_open' issue, got %s", result.IssuesFound[0].IssueType)
	}
}

func TestCheckIntegrityResult(t *testing.T) {
	repair := NewDatabaseRepair(nil)

	result, _ := repair.CheckIntegrity(false)

	duration := result.GetDuration()
	if duration < 0 {
		t.Error("Duration should be positive")
	}

	summary := result.GetSummary()
	if summary == "" {
		t.Error("Summary should not be empty")
	}
}

func TestRepairNilDatabase(t *testing.T) {
	repair := NewDatabaseRepair(nil)

	result, err := repair.Repair(false, true)

	if err == nil {
		t.Error("Expected error when database is nil")
	}

	if result == nil {
		t.Error("Result should not be nil even on error")
	}
}

func TestRebuildIndexesNilDatabase(t *testing.T) {
	repair := NewDatabaseRepair(nil)

	result, err := repair.RebuildIndexes(true)

	if err == nil {
		t.Error("Expected error when database is nil")
	}

	if result == nil {
		t.Error("Result should not be nil even on error")
	}
}

func TestDefragmentNilDatabase(t *testing.T) {
	repair := NewDatabaseRepair(nil)

	result, err := repair.Defragment(true)

	if err == nil {
		t.Error("Expected error when database is nil")
	}

	if result == nil {
		t.Error("Result should not be nil even on error")
	}
}

func TestRepairResultDuration(t *testing.T) {
	// Create mock database
	repair := NewDatabaseRepair(nil)

	_, _ = repair.Repair(false, true)

	// This tests nil database path so result should exist but be incomplete
	repair2 := NewDatabaseRepair(nil)
	result, _ := repair2.Repair(false, true)

	if result != nil {
		duration := result.GetDuration()
		if duration < 0 {
			t.Error("Duration should be non-negative")
		}
	}
}

func TestIntegrityIssueSeverity(t *testing.T) {
	repair := NewDatabaseRepair(nil)

	result, _ := repair.CheckIntegrity(false)

	if len(result.IssuesFound) > 0 {
		issue := result.IssuesFound[0]
		if issue.Severity == "" {
			t.Error("Issue severity should be set")
		}

		validSeverities := map[string]bool{"info": true, "warning": true, "error": true}
		if !validSeverities[issue.Severity] {
			t.Errorf("Invalid severity: %s", issue.Severity)
		}
	}
}

func TestRepairActionSuccess(t *testing.T) {
	repair := NewDatabaseRepair(nil)

	result, _ := repair.Repair(false, true)

	if result != nil {
		for _, action := range result.ActionsPerformed {
			if action.ActionType == "" {
				t.Error("Action type should be set")
			}
			if action.Description == "" {
				t.Error("Action description should be set")
			}
		}
	}
}

func TestCheckIntegritySummary(t *testing.T) {
	repair := NewDatabaseRepair(nil)

	result, _ := repair.CheckIntegrity(false)

	summary := result.GetSummary()
	if summary == "" {
		t.Error("Summary should not be empty")
	}

	if result.DatabaseValid && summary != "Database is valid - no issues found" {
		t.Error("Valid database should have appropriate summary")
	}

	if !result.DatabaseValid && len(result.IssuesFound) > 0 {
		if !contains(summary, result.IssuesFound[0].IssueType) {
			t.Error("Summary should contain issue type")
		}
	}
}

func TestRepairResultSummary(t *testing.T) {
	repair := NewDatabaseRepair(nil)

	result, _ := repair.Repair(false, true)

	if result != nil {
		summary := result.GetSummary()
		if summary == "" {
			t.Error("Summary should not be empty")
		}

		// Should mention number of actions or "No repair actions needed"
		if len(result.ActionsPerformed) == 0 {
			if summary != "No repair actions needed" {
				t.Errorf("Expected 'No repair actions needed', got %s", summary)
			}
		}
	}
}

func TestIntegrityCheckTimestamps(t *testing.T) {
	repair := NewDatabaseRepair(nil)

	result, _ := repair.CheckIntegrity(false)

	if result.StartTime.IsZero() {
		t.Error("Start time should be set")
	}

	if result.EndTime.IsZero() {
		t.Error("End time should be set")
	}

	if result.EndTime.Before(result.StartTime) {
		t.Error("End time should be after start time")
	}
}

func TestRepairResultTimestamps(t *testing.T) {
	repair := NewDatabaseRepair(nil)

	result, _ := repair.Repair(false, true)

	if result != nil {
		if result.StartTime.IsZero() {
			t.Error("Start time should be set")
		}

		if result.EndTime.IsZero() {
			t.Error("End time should be set")
		}

		if result.EndTime.Before(result.StartTime) {
			t.Error("End time should be after start time")
		}
	}
}

func TestDryRunMode(t *testing.T) {
	repair := NewDatabaseRepair(nil)

	result, _ := repair.Repair(false, true)

	if result != nil {
		if !result.DryRun {
			t.Error("DryRun flag should be true when specified")
		}
	}

	result2, _ := repair.Repair(false, false)

	if result2 != nil {
		if result2.DryRun {
			t.Error("DryRun flag should be false when not specified")
		}
	}
}

// Helper function
func contains(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
