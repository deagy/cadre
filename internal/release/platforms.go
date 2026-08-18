// Package release holds the release-asset naming contract for the compiled
// cadre binary.
//
// Single source of truth for the supported GOOS/GOARCH matrix and the
// archive/executable naming pattern. Makefile's cross-build recipe and
// DISTRIBUTION.md are each checked against this, never the other way around,
// so changing the platform set has to touch this file deliberately.
//
// Deliberately free of import-time side effects: no filesystem and no network
// access at package scope, so importing this can never fail for a reason
// unrelated to the platform contract itself.
//
// Ported from plugin/tools/binary_platforms.py. That module's docstring said
// the release workflow imported SupportedPlatforms from it; the workflow only
// ever named it in a comment. A comment cannot fail, which is the whole
// reason the guard below reads the Makefile and the doc rather than trusting
// that they agree.
package release

import "fmt"

// Platform is one GOOS/GOARCH pair in the release matrix.
type Platform struct {
	GOOS   string
	GOARCH string
}

func (p Platform) String() string { return p.GOOS + "/" + p.GOARCH }

// SupportedPlatforms is hand-reviewed, not derived from any other file.
//
// windows/arm64 is excluded deliberately rather than merely absent. GitHub's
// hosted windows-latest runner is x64, its gcc is x86_64 MinGW and cannot emit
// ARM64 Windows objects, and CGO_ENABLED=1 -- required by the knowledge store
// -- turns that mismatch into a hard build failure rather than a silently
// stubbed binary. Since the release workflow's publish job depends on every
// build leg succeeding, a windows/arm64 leg would fail every future release,
// not just skip one platform. See DISTRIBUTION.md's "Platform support".
var SupportedPlatforms = []Platform{
	{"linux", "amd64"},
	{"linux", "arm64"},
	{"darwin", "amd64"},
	{"darwin", "arm64"},
	{"windows", "amd64"},
}

// ArchiveExtension is the archive format published for a platform.
func ArchiveExtension(goos string) string {
	if goos == "windows" {
		return "zip"
	}
	return "tar.gz"
}

// ExecutableName is the single executable the CLI's archive contains.
func ExecutableName(goos string) string {
	return ExecutableNameFor(ProgramCLI, goos)
}

// Programs are the binaries this repository publishes per platform.
//
// The kernel is a separate program, not a second executable inside the CLI's
// archive, because it is separately versioned: providers declare a
// kernel_compatibility window against kernel.Version, which moves
// independently of the CLI's own version. One archive carrying both would
// force the two version lines back together.
const (
	ProgramCLI    = "cadre"
	ProgramKernel = "agentic-sdlc"
)

// Programs is every published program, in the order a release builds them.
var Programs = []string{ProgramCLI, ProgramKernel}

// TagPrefix is the release-tag namespace each program publishes under.
//
// The names differ from the binaries deliberately and cannot be derived from
// them: the CLI publishes under cli-v while the packaged plugin publishes
// under plugin-v, and both come out of the same repository. Recorded here so
// the workflow and the guards read one list rather than two.
var TagPrefix = map[string]string{
	ProgramCLI:    "cli-v",
	ProgramKernel: "kernel-v",
}

// ArchiveName renders the published asset name for a program on a platform.
func ArchiveName(program, goos, goarch, version string) string {
	return fmt.Sprintf("%s-v%s-%s-%s.%s", program, version, goos, goarch, ArchiveExtension(goos))
}

// ExecutableNameFor is the single executable a program's archive contains.
func ExecutableNameFor(program, goos string) string {
	if goos == "windows" {
		return program + ".exe"
	}
	return program
}
