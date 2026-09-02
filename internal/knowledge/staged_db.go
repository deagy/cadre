package knowledge

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store is the staged-record store: proposals, their dispositions, their
// import provenance and their deletion evidence.
//
// It used to be the retrieval engine as well -- one type, one schema, one
// database file holding both the corpus and the governance record over it.
// P2 recorded that these are different concerns and should not be merged;
// this is the separation. Retrieval is recall's, reached through
// internal/retrieval; what is left here is the record of what a steward
// decided, which no store implementation owns.
type Store struct {
	db   *sql.DB
	path string
}

// StagedDatabaseFile is the staged store's own database, beside whatever
// store the knowledge config names.
//
// Its own file rather than the configured database: that path now names a
// recall store, and cadre's governance tables have no business inside a
// database recall's own backup, restore and migration tooling operates on
// without knowing they are there.
const StagedDatabaseFile = "staged-records.db"

// StagedDatabasePath returns the staged store for a resolved config.
func StagedDatabasePath(cfg *Config) string {
	return filepath.Join(filepath.Dir(cfg.Database), StagedDatabaseFile)
}

// BusyTimeout is how long a connection waits for a lock before giving up.
//
// Without it SQLite returns SQLITE_BUSY the instant a lock is held, and two
// processes opening the same store at once means one of them simply fails.
// The store is opened per CLI invocation, so "at once" is the ordinary case.
const BusyTimeout = 5 * time.Second

// dsn builds the connection string, carrying every pragma so the pool cannot
// hand out a connection configured differently from its siblings.
//
// database/sql hands out connections from a pool, so a `PRAGMA` run via
// db.Exec applies to whichever connection served it and to no other. A
// busy_timeout set that way is absent on the next connection, which is how
// two concurrent writers failed with "database is locked" -- the timeout was
// set on one connection while journal_mode blocked on another.
func dsn(dbPath string) string {
	return fmt.Sprintf("file:%s?_pragma=busy_timeout(%d)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)",
		dbPath, BusyTimeout.Milliseconds())
}

// OpenStaged opens or creates the staged-record store.
//
// The driver is modernc.org/sqlite -- pure Go, the same one recall uses. The
// engine this package used to hold needed cgo's mattn/go-sqlite3, so a
// CGO_ENABLED=0 build of cadre linked cleanly and then failed at the first
// query with "go-sqlite3 requires cgo to work. This is a stub". The file
// format is identical between the two drivers; what changes is that the
// staged workflow now runs in the default build.
func OpenStaged(dbPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("cannot create knowledge store directory: %w", err)
	}

	db, err := sql.Open("sqlite", dsn(dbPath))
	if err != nil {
		return nil, fmt.Errorf("cannot open staged-record store: %w", err)
	}
	if err := initStagedSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, path: dbPath}, nil
}

// initStagedSchema creates the staged tables. Idempotent.
//
// Retries while the database is locked, which busy_timeout alone does not
// cover: SQLite does not apply it to a lock upgrade inside a transaction, so
// a concurrent writer fails immediately rather than waiting. Retrying is
// correct rather than convenient -- the statement is idempotent, so a retry
// either finds the work done or does it.
func initStagedSchema(db *sql.DB) error {
	if err := execWithBusyRetry(db, stagedSchema); err != nil {
		return fmt.Errorf("cannot initialize schema: %w", err)
	}
	// CREATE TABLE IF NOT EXISTS does nothing to a table that already
	// exists, so a column added to stagedSchema never reaches a store
	// written before it. This is the open path -- every command comes
	// through here -- and the migration has to run on it, not only on
	// InstallStagedSchema, which nothing on this path calls.
	if err := migrateAdditiveStagedColumns(db); err != nil {
		return err
	}
	return nil
}

func execWithBusyRetry(db *sql.DB, statement string) error {
	deadline := time.Now().Add(BusyTimeout)
	delay := 10 * time.Millisecond
	for {
		_, err := db.Exec(statement)
		if err == nil {
			return nil
		}
		if !isLockedError(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(delay)
		if delay < 200*time.Millisecond {
			delay *= 2
		}
	}
}

func isLockedError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "database is locked") ||
		strings.Contains(message, "SQLITE_BUSY")
}

// Close closes the database connection. Safe to call multiple times.
func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// Path is the database file this store is backed by.
func (s *Store) Path() string { return s.path }

// nowISO is the timestamp format every staged row carries.
func nowISO() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

// ErrNoLegacyStagedRecords: the named legacy store holds no staged tables.
var ErrNoLegacyStagedRecords = errors.New("knowledge: no staged records to migrate")

// MigrateStagedRecords copies staged rows out of a legacy combined store.
//
// Staged records used to live in the same database as the corpus. When that
// database became recall's, they were left inside a file cadre no longer
// opens with its own schema -- reachable, but only by a version of cadre that
// still had the engine. This copies the four tables across, once, and leaves
// the legacy file untouched: a migration that deletes its source cannot be
// re-run after someone notices it went wrong.
func MigrateStagedRecords(legacyPath, stagedPath string) (int, error) {
	if _, err := os.Stat(legacyPath); err != nil {
		return 0, ErrNoLegacyStagedRecords
	}

	legacy, err := sql.Open("sqlite", dsn(legacyPath))
	if err != nil {
		return 0, fmt.Errorf("cannot open the legacy store: %w", err)
	}
	defer func() { _ = legacy.Close() }()

	var present int
	if err := legacy.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='staged_records'`,
	).Scan(&present); err != nil || present == 0 {
		return 0, ErrNoLegacyStagedRecords
	}

	store, err := OpenStaged(stagedPath)
	if err != nil {
		return 0, err
	}
	defer func() { _ = store.Close() }()

	if _, err := store.db.Exec(`ATTACH DATABASE ? AS legacy`, legacyPath); err != nil {
		return 0, fmt.Errorf("cannot attach the legacy store: %w", err)
	}
	defer func() { _, _ = store.db.Exec(`DETACH DATABASE legacy`) }()

	copied := 0
	for _, table := range []string{
		"staged_records", "staged_record_dispositions",
		"staged_record_imports", "staged_record_deletions",
	} {
		result, err := store.db.Exec(fmt.Sprintf(
			`INSERT OR IGNORE INTO main.%s SELECT * FROM legacy.%s`, table, table))
		if err != nil {
			return copied, fmt.Errorf("cannot copy %s: %w", table, err)
		}
		if affected, err := result.RowsAffected(); err == nil {
			copied += int(affected)
		}
	}
	return copied, nil
}

// StagedDriverAvailable reports whether the sqlite driver this package uses
// is usable in the running binary. A nil error means the staged store will
// work; `cadre doctor` shows the operator what it says.
//
// A runtime probe rather than a build-tag constant, matching the reasoning
// this package has always applied here: a build tag would also drop the
// guarded code from `go build`, `go vet` and golangci-lint, which is how an
// unused-import error once reached CI unseen.
func StagedDriverAvailable() error {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.Ping()
}
