package cli

import (
	"github.com/deagy/cadre/cli/internal/version"
)

// CLIVersion returns Cadre's distribution version.
//
// The resolution lives in internal/version because internal/generators needs
// the same answer and cannot import this package (internal/cli imports it).
// Two copies of this parse already existed, and moving the marker broke one
// of them silently.
func CLIVersion(repoRoot string) (string, error) {
	return version.Resolve(repoRoot)
}
