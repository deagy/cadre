package knowledge

import (
	"errors"
	"testing"
)

// Backup and restore refuse, and nothing is recorded as though they had run.
//
// This replaces four tests that asserted the simulation was intact:
// TestCreateBackup checked for a backup id and a history entry reading
// "completed" with 1000 messages; TestRestoreFromBackup checked that restore
// returned nil, which it did by doing nothing at all; TestBackupHistory
// checked that three backups that never happened were all remembered; and
// TestRecoveryPointVerification checked rp.Verified, which was assigned true
// unconditionally. Together they made the fabrication look deliberate and
// protected it from removal -- a passing suite is not evidence of a working
// feature if the suite asserts the placeholder.
func TestBackupAndRestoreRefuseRatherThanReportSuccess(t *testing.T) {
	dr := NewDisasterRecovery("/backups")

	backupID, err := dr.CreateBackup(1000, 500, 1024*1024)
	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf("CreateBackup error = %v, want ErrNotImplemented", err)
	}
	if backupID != "" {
		t.Errorf("CreateBackup returned id %q; a refused backup must not be identified", backupID)
	}
	if err := dr.RestoreFromBackup("backup-anything"); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("RestoreFromBackup error = %v, want ErrNotImplemented", err)
	}

	// Nothing may be recorded as a backup. History that remembers a refused
	// backup is what let `backup create` be believed in the first place.
	if history := dr.GetBackupHistory(); len(history) != 0 {
		t.Errorf("backup history has %d entries after a refused backup, want 0", len(history))
	}
	dr.mu.RLock()
	points := len(dr.recoveryPoints)
	dr.mu.RUnlock()
	if points != 0 {
		t.Errorf("%d recovery points exist after a refused backup, want 0", points)
	}
}
func TestDisasterRecoveryCreation(t *testing.T) {
	dr := NewDisasterRecovery("/backups")
	if dr == nil {
		t.Fatal("Failed to create disaster recovery manager")
		return
	}

	if dr.backupLocation != "/backups" {
		t.Errorf("Expected backup location '/backups', got %s", dr.backupLocation)
	}
}

func TestRestoreFromInvalidBackup(t *testing.T) {
	dr := NewDisasterRecovery("/backups")

	err := dr.RestoreFromBackup("invalid-backup")
	if err == nil {
		t.Error("Expected error when restoring invalid backup")
	}
}
