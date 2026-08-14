package cli

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// requireSQLite skips the calling test when the cgo-backed sqlite3 driver is
// not linked into the test binary.
//
// This is a *runtime* guard on purpose. mattn/go-sqlite3 registers its driver
// only under cgo, so with CGO_ENABLED=0 every SQLite-backed test here fails
// (or panics) on a driver that does not exist. Guarding the failure with a
// `//go:build cgo` tag would also remove these files from `go build`,
// `go vet` and golangci-lint -- which is exactly how an unused-import error
// in this package once reached CI unseen. Skipping at run time keeps every
// line of test source in the default build while still letting a
// no-C-toolchain checkout run `go test ./...` cleanly.
//
// CI builds with cgo enabled, so this never skips there.
func requireSQLite(t *testing.T) {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Skipf("sqlite3 driver unavailable (built without cgo?): %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Ping(); err != nil {
		t.Skipf("sqlite3 driver unavailable (built without cgo?): %v", err)
	}
}
