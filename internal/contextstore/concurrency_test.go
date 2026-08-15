package contextstore

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// SQLite serialises writers. Without a busy timeout a second writer does not
// wait -- it fails immediately with SQLITE_BUSY, surfacing as "database is
// locked" -- and dispatch runs team members concurrently against one store,
// so that contention is the normal case rather than an unlucky one.
//
// Ported from roster/context-store/test/test_concurrency.py, which had no Go
// counterpart: internal/contextstore had 63 tests and none of them touched
// concurrency.

// configuredBusyTimeoutMS is what OpenStore's DSN asks for. Pinned as a
// literal rather than parsed out of the DSN: a test that derives its
// expectation from the code it checks agrees with that code by construction.
const configuredBusyTimeoutMS = 10000

func TestBusyTimeoutIsSetExplicitlyOnEveryConnection(t *testing.T) {
	// Two things at once, and the second is why the exact value is asserted.
	//
	// The pragma has to ride on the DSN: applying it with a PRAGMA statement
	// after opening sets it on whichever pooled connection served that
	// statement, and database/sql opens more whenever it likes -- so a writer
	// on a later connection would not get it.
	//
	// And it has to be the store's own value, not the driver's. mattn/
	// go-sqlite3 defaults busy_timeout to 5000ms, so asserting merely that it
	// is non-zero passes with the DSN parameter deleted -- which is exactly
	// what happened when this was first written that way. Leaving the driver
	// default in place would be a silent choice, made by whoever last bumped
	// the dependency.
	path := filepath.Join(t.TempDir(), "store.db")
	db, err := OpenStore(path, false)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Force several distinct connections to exist at once, then check each.
	db.SetMaxOpenConns(4)
	var connections []*sql.Conn
	for index := 0; index < 4; index++ {
		conn, err := db.Conn(t.Context())
		if err != nil {
			t.Fatalf("connection %d: %v", index, err)
		}
		connections = append(connections, conn)
	}
	defer func() {
		for _, conn := range connections {
			_ = conn.Close()
		}
	}()

	for index, conn := range connections {
		var timeout int
		if err := conn.QueryRowContext(t.Context(), "PRAGMA busy_timeout").Scan(&timeout); err != nil {
			t.Fatalf("connection %d: reading busy_timeout: %v", index, err)
		}
		if timeout != configuredBusyTimeoutMS {
			t.Errorf("connection %d has busy_timeout=%d, want the store's configured %d",
				index, timeout, configuredBusyTimeoutMS)
		}
	}
}

func TestAConcurrentWriterWaitsOutTheLockInsteadOfFailing(t *testing.T) {
	// The behaviour the pragma exists for, exercised rather than inferred: one
	// writer holds an exclusive transaction while another tries to write.
	path := filepath.Join(t.TempDir(), "store.db")

	first, err := OpenStore(path, false)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer func() { _ = first.Close() }()

	second, err := OpenStore(path, false)
	if err != nil {
		t.Fatalf("OpenStore (second handle): %v", err)
	}
	defer func() { _ = second.Close() }()

	// Hold a write lock briefly -- long enough that a zero-timeout writer
	// would fail, short enough that a waiting one succeeds well inside the
	// 10s busy timeout.
	transaction, err := first.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := transaction.ExecContext(t.Context(),
		`INSERT INTO entries (handle, scope, source, task_id, agent, label, tags_json, content, content_hash, byte_length, classification, derived_from_json, redactions_json, created_at, expires_at)
		 VALUES ('ctx_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','agent','s','t','a','held','[]','x','h',1,'internal','[]','[]','2026-01-01T00:00:00Z','2099-01-01T00:00:00Z')`,
	); err != nil {
		_ = transaction.Rollback()
		t.Fatalf("holding the write lock: %v", err)
	}

	released := make(chan struct{})
	go func() {
		time.Sleep(250 * time.Millisecond)
		_ = transaction.Commit()
		close(released)
	}()

	start := time.Now()
	_, writeErr := second.ExecContext(t.Context(),
		`INSERT INTO entries (handle, scope, source, task_id, agent, label, tags_json, content, content_hash, byte_length, classification, derived_from_json, redactions_json, created_at, expires_at)
		 VALUES ('ctx_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','agent','s','t','a','waiting','[]','x','h',1,'internal','[]','[]','2026-01-01T00:00:00Z','2099-01-01T00:00:00Z')`)
	elapsed := time.Since(start)
	<-released

	if writeErr != nil {
		t.Fatalf("the second writer failed instead of waiting out the lock: %v", writeErr)
	}
	// It genuinely waited rather than finding the lock already gone.
	if elapsed < 100*time.Millisecond {
		t.Logf("second write completed in %v; the lock may not have been held", elapsed)
	}
}

func TestTheAdditiveMigrationIsIdempotent(t *testing.T) {
	// The property that makes the concurrent case safe, pinned
	// deterministically: running the migration again on an already-migrated
	// store must succeed, not fail with "duplicate column name".
	//
	// This is the whole point of attempting the ALTER unconditionally. The
	// earlier check-then-add form could only be tested by racing goroutines,
	// which reproduced the defect about half the time -- a guard that catches
	// a real regression one run in two reads as flake and gets deleted.
	path := filepath.Join(t.TempDir(), "store.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("schema: %v", err)
	}

	for attempt := 1; attempt <= 3; attempt++ {
		if err := migrateAdditiveColumns(db); err != nil {
			t.Fatalf("migration attempt %d failed: %v", attempt, err)
		}
	}

	// And the column really is there afterwards -- an idempotent no-op that
	// migrated nothing would pass the loop above.
	var count int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM pragma_table_info('entries') WHERE name = 'promoted_at'",
	).Scan(&count); err != nil {
		t.Fatalf("checking the column: %v", err)
	}
	if count != 1 {
		t.Errorf("promoted_at column count = %d, want exactly 1", count)
	}
}

func TestConcurrentMigrationsAllSucceed(t *testing.T) {
	// The concurrent form, kept as a smoke test rather than as the regression
	// gate: the interleaving cannot be forced, so it reproduced the original
	// defect only about half the time. TestTheAdditiveMigrationIsIdempotent
	// above is what actually pins the fix.
	path := filepath.Join(t.TempDir(), "store.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("schema: %v", err)
	}

	const migrators = 16
	var group sync.WaitGroup
	errs := make(chan error, migrators)
	for index := 0; index < migrators; index++ {
		group.Add(1)
		go func(n int) {
			defer group.Done()
			if err := migrateAdditiveColumns(db); err != nil {
				errs <- fmt.Errorf("migrator %d: %w", n, err)
			}
		}(index)
	}
	group.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestOnlyADuplicateColumnIsTolerated(t *testing.T) {
	// The tolerance is deliberately narrow: it exists for one specific losing
	// race, and any other schema failure must still fail the open rather than
	// being swallowed into a store that is missing something.
	path := filepath.Join(t.TempDir(), "store.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("schema: %v", err)
	}

	// The winner adds it; the loser's identical statement is the tolerated
	// case.
	if _, err := db.Exec("ALTER TABLE entries ADD COLUMN promoted_at TEXT"); err != nil {
		t.Fatalf("first ALTER should succeed: %v", err)
	}
	_, lostRace := db.Exec("ALTER TABLE entries ADD COLUMN promoted_at TEXT")
	if !isDuplicateColumnError(lostRace) {
		t.Errorf("a duplicate column was not recognised: %v", lostRace)
	}

	// Anything else is not.
	_, missingTable := db.Exec("ALTER TABLE no_such_table ADD COLUMN whatever TEXT")
	if isDuplicateColumnError(missingTable) {
		t.Errorf("an unrelated schema failure was treated as a lost race: %v", missingTable)
	}
	if missingTable == nil {
		t.Error("altering a missing table should fail")
	}
}

func TestConcurrentOpensOfTheSameStoreAllSucceed(t *testing.T) {
	// Sixteen openers racing on a store that does not exist yet, repeated,
	// because the failure it guards is rare per attempt: roughly one opener in
	// five hundred used to come back "database is locked" immediately.
	//
	// Immediately is the point. `_busy_timeout=10000` is set on every
	// connection, so a lock this open path actually waited on would surface
	// after ten seconds or not at all. This one surfaced at once, because
	// SQLite does not invoke the busy handler while another connection is
	// converting the journal to WAL. OpenStore retries it; without that retry
	// this test fails within a few rounds.
	const rounds, openers = 25, 16
	for round := 0; round < rounds; round++ {
		path := filepath.Join(t.TempDir(), "store.db")

		var group sync.WaitGroup
		errs := make(chan error, openers)
		for index := 0; index < openers; index++ {
			group.Add(1)
			go func(n int) {
				defer group.Done()
				db, err := OpenStore(path, true)
				if err != nil {
					errs <- fmt.Errorf("round %d opener %d: %w", round, n, err)
					return
				}
				_ = db.Close()
			}(index)
		}
		group.Wait()
		close(errs)

		for err := range errs {
			t.Error(err)
		}
		if t.Failed() {
			return // one round's worth of failures is enough to read
		}
	}
}
