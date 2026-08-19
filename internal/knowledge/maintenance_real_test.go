package knowledge

import (
	"os"
	"testing"
)

// Vacuuming must reclaim space, not report that it did.
//
// `cadre knowledge maintenance vacuum` used to be a 100ms sleep under a comment
// reading "Simulate vacuum execution", followed by CompleteMaintenanceTask --
// so it recorded a completed maintenance task and printed "Vacuum completed"
// having issued no VACUUM at all. It was the eleventh command found reporting
// work it had not done.
//
// Asserted by file size rather than by a returned status, because a status is
// exactly what the simulation produced. Filling the database and deleting the
// rows leaves free pages that only a real VACUUM returns to the filesystem, so
// this fails against any implementation that merely says it succeeded.
func TestDefragmentActuallyReclaimsSpace(t *testing.T) {
	path := t.TempDir() + "/vacuum.db"
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Closed explicitly below, before the size is measured.
	padding := make([]byte, 2048)
	for i := range padding {
		padding[i] = 'x'
	}
	for i := 0; i < 300; i++ {
		if _, err := store.db.Exec(`INSERT INTO messages
			(id, source, conversation_id, source_message_id, role, content, content_hash,
			 classification, injection_risk, redactions_json, metadata_json, ingested_at)
			VALUES (?, 'bulk', 'c1', ?, 'user', ?, ?, 'internal', 0, '[]', '{}', '2026-08-19T00:00:00Z')`,
			i, i, string(padding), i); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	if _, err := store.db.Exec("DELETE FROM messages"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	before := fileSize(t, path)
	if _, err := NewDatabaseRepair(store.db).Defragment(false); err != nil {
		t.Fatalf("Defragment: %v", err)
	}

	// Measured after closing. The store runs in WAL mode, so VACUUM writes the
	// rebuilt database through the write-ahead log and the main file does not
	// shrink until a checkpoint -- which happens on close. Measuring before
	// that reports the pre-vacuum size and makes a working VACUUM look inert.
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	after := fileSize(t, path)

	if after >= before {
		t.Errorf("database is %d bytes after vacuuming %d bytes of mostly free pages; "+
			"nothing was reclaimed, so no VACUUM ran", after, before)
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Size()
}
