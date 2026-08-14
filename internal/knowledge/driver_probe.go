package knowledge

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3"
)

// DriverAvailable reports whether the cgo-backed sqlite3 driver this package
// depends on is actually usable in the running binary. A nil error means the
// knowledge store will work; a non-nil error carries the driver's own
// explanation, which is what `cadre doctor` shows the operator.
//
// This is a *runtime* probe rather than a `//go:build cgo` constant, matching
// the reasoning already recorded on requireSQLite in this package's tests: a
// build tag would also drop the guarded code from `go build`, `go vet` and
// golangci-lint, which is how an unused-import error once reached CI unseen.
// A probe costs one in-memory open and keeps every line in the default build.
//
// Why this needs surfacing at all: mattn/go-sqlite3 registers its driver only
// under cgo. Built with CGO_ENABLED=0 the binary still compiles and links --
// the package ships a cgo-less stub -- so nothing fails until the first real
// query, at which point every `cadre knowledge` call returns "Binary was
// compiled with 'CGO_ENABLED=0', go-sqlite3 requires cgo to work. This is a
// stub". bin/cadre prefers a cgo build and silently falls back to a cgo-less
// one when no C toolchain is present, so a degraded binary is a supported
// outcome rather than a bug -- but the operator has to be able to find out
// they have one without first hitting the runtime error.
func DriverAvailable() error {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return db.Ping()
}
