package knowledge

import (
	"database/sql"
	"testing"
	"time"
)

// A deletion whose evidence loses a lock race is a permanent hole.
//
// The content is removed before this row is written, and re-running the
// command refuses because there is nothing left to delete. So unlike every
// other write in this package, "it failed, try again" is not available.
//
// The lock is held for longer than BusyTimeout deliberately. A held lock
// shorter than that proves nothing: the connection string sets
// busy_timeout(5000), so the driver alone would ride it out and the test would
// pass with or without the retry. It has to outlast the ordinary budget to ask
// whether the evidence write has one of its own.
func TestDeletionEvidenceSurvivesALockHeldPastTheOrdinaryTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("holds a write lock for longer than BusyTimeout")
	}

	path := t.TempDir() + "/staged.db"
	store, err := OpenStaged(path)
	if err != nil {
		t.Fatalf("cannot open the store: %v", err)
	}
	defer func() { _ = store.Close() }()

	// One uncontended deletion first, so the table exists.
	//
	// Without this the test does not measure what it says. `CREATE TABLE IF
	// NOT EXISTS` runs before the INSERT and is the first statement to want
	// the write lock, so on a fresh store it absorbs all the contention and
	// the INSERT never meets the competitor at all. Two separate mutations --
	// removing the INSERT's retry entirely, and cutting both budgets back --
	// each passed against the earlier version of this test, which is how the
	// gap showed up.
	if err := store.RecordIngestedDeletion(IngestedDeletion{
		DocumentID: "doc-that-creates-the-table", ChunksRemoved: 1,
		Reason: "so the schema exists before the contended write", DeletedBy: "setup",
	}); err != nil {
		t.Fatalf("the uncontended setup deletion failed: %v", err)
	}

	// A second connection, so this is a real lock between two database
	// handles rather than two goroutines sharing one pool.
	competitor, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("cannot open the competing connection: %v", err)
	}
	defer func() { _ = competitor.Close() }()

	if _, err := competitor.Exec(
		"CREATE TABLE IF NOT EXISTS lock_probe (id INTEGER)"); err != nil {
		t.Fatalf("cannot create the probe table: %v", err)
	}

	// Longer than two attempts on the ordinary budget.
	//
	// The retry loop checks its deadline only after an Exec that has already
	// blocked for the driver's busy_timeout, so a budget of BusyTimeout buys
	// two attempts and roughly twice BusyTimeout of real waiting, not one and
	// not five seconds. A lock held for eight seconds is therefore survived
	// with or without the longer budget, which is why the first version of
	// this test could not tell the two apart.
	hold := 2*BusyTimeout + 3*time.Second
	locked := make(chan struct{})
	released := make(chan struct{})

	go func() {
		defer close(released)
		tx, err := competitor.Begin()
		if err != nil {
			close(locked)
			return
		}
		// BEGIN alone takes no write lock; SQLite defers it until the first
		// write. Without this INSERT the "competitor" holds nothing and the
		// test measures an uncontended write.
		if _, err := tx.Exec("INSERT INTO lock_probe (id) VALUES (1)"); err != nil {
			close(locked)
			_ = tx.Rollback()
			return
		}
		close(locked)
		time.Sleep(hold)
		_ = tx.Commit()
	}()

	<-locked
	started := time.Now()

	err = store.RecordIngestedDeletion(IngestedDeletion{
		DocumentID:    "doc-under-contention",
		ChunksRemoved: 11,
		Reason:        "a colleague asked for their content to be removed",
		DeletedBy:     "someone",
	})
	if err != nil {
		t.Fatalf("the deletion evidence was lost to a lock held for %s: %v\n\n"+
			"The content this describes is already gone, so nothing will write "+
			"this row later. EvidenceBusyTimeout is what makes it wait.", hold, err)
	}

	// A pass in less time than the lock was held would mean the lock was never
	// taken -- the test would be green and testing nothing.
	if waited := time.Since(started); waited < hold {
		t.Fatalf("the write returned after %s but the lock was meant to be held for %s; "+
			"the contention this test exists to create did not happen", waited, hold)
	}

	<-released

	recorded, err := store.IngestedDeletions("doc-under-contention")
	if err != nil {
		t.Fatalf("cannot read the evidence back: %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("expected one deletion record, found %d", len(recorded))
	}
	if recorded[0].ChunksRemoved != 11 {
		t.Fatalf("the record says %d chunks, the deletion removed 11",
			recorded[0].ChunksRemoved)
	}
}
