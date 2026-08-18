// Package cadre exists for one reason: to embed the VERSION marker into every
// binary built from this module.
//
// A released archive contains the executable and nothing else. Version
// resolution reads a marker file beside an installation root -- <repo>/VERSION
// in a checkout, <plugin>/suite/VERSION in a packaged plugin,
// <prefix>/share/cadre/VERSION in a wheel -- and a lone binary has none of
// those. `cadre --version`, the first thing anyone runs to check a download,
// failed on the very asset the release publishes.
//
// Embedded rather than stamped with -ldflags. There are four build sites --
// the Makefile's cross-build, release.yml's matrix, bin/cadre's shim, and a
// plain `go run` -- and a flag is something each of them has to remember. A
// missing flag produces a binary that reports no version, which is the same
// silent-in-the-safe-direction failure this repository keeps finding. //go:embed
// needs nothing at the call site and cannot be forgotten.
//
// This package sits at the module root because go:embed patterns cannot escape
// their package directory, and VERSION belongs at the root: release.yml reads
// it, the wheel build reads it, and moving it to suit the compiler would be the
// tail wagging the dog.
package cadre

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var marker string

// Version is the version this binary was built from.
//
// Callers should prefer a marker file when one is present: a packaged plugin
// or an installed wheel must report *its own* version, not the version of
// whatever binary happens to be reading it. This is the answer when there is
// no marker to read.
func Version() string {
	return strings.TrimSpace(marker)
}
