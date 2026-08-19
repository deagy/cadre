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
	// ProgramEngine drives a task through the lifecycle gates. It ships in
	// the CLI's release rather than its own: it links the same sqlite
	// checkpointer, so it needs cgo and a native host per platform, which is
	// exactly the matrix the CLI already builds.
	ProgramEngine = "agentic-sdlc-engine"
)

// Programs is every published program, in the order a release builds them.
var Programs = []string{ProgramCLI, ProgramKernel, ProgramEngine}

// TagPrefix is the release-tag namespace each program publishes under.
//
// The names differ from the binaries deliberately and cannot be derived from
// them: the CLI publishes under cli-v while the packaged plugin publishes
// under plugin-v, and both come out of the same repository. Recorded here so
// the workflow and the guards read one list rather than two.
var TagPrefix = map[string]string{
	ProgramCLI:    "cli-v",
	ProgramKernel: "kernel-v",
	// The engine shares the CLI's tag because it ships in the same release:
	// one archive set, one version, one thing to install.
	ProgramEngine: "cli-v",
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

// unpublishable records platforms a specific program cannot ship, and why.
//
// The matrix above is what this repository contracts for in general. One
// program can still be unable to reach a corner of it, and that is a property
// of the program rather than of the platform: the kernel cross-compiles to
// every entry from a single Linux runner, while the CLI links the knowledge
// store's sqlite and so needs CGO_ENABLED=1 and a native host per platform.
//
// Recorded as data rather than prose so the Makefile, the workflow and the
// documentation are all checked against one list. An entry here is a
// deliberate reduction of what a program publishes, so it carries its reason
// with it.
var unpublishable = map[string]map[Platform]string{
	ProgramCLI: {
		{GOOS: "darwin", GOARCH: "amd64"}: "the CLI needs cgo, so darwin/amd64 needs a " +
			"native Intel macOS runner. GitHub retired macos-13 and the surviving Intel " +
			"labels (macos-15-intel, macos-26-intel) are a separate, more expensive SKU. " +
			"Dropped deliberately: Apple Silicon Macs are served by darwin/arm64, and an " +
			"Intel Mac cannot run that binary under Rosetta -- Rosetta translates x86_64 " +
			"for Apple Silicon, not the reverse. Intel macOS users must build from source",
		{GOOS: "linux", GOARCH: "arm64"}: "the CLI needs cgo, so linux/arm64 needs either a native " +
			"arm64 runner or a cross toolchain installed with apt. The apt route failed four " +
			"releases running -- `apt-get` hung or errored against the Ubuntu mirror every time, " +
			"burning the job budget before the build started and skipping the publish with every " +
			"other platform already built. Dropped deliberately rather than made the release's " +
			"most fragile dependency. The kernel still publishes linux/arm64: it does not link " +
			"cgo and cross-compiles from a single runner with no toolchain to install. arm64 " +
			"Linux users build from source, or install the pip wheel if one is published for " +
			"their platform",
	},
	ProgramEngine: {
		{GOOS: "linux", GOARCH: "arm64"}: "excluded with the CLI, for the same reason and on the " +
			"same leg: it is built beside the CLI on that runner and shares its toolchain",
		{GOOS: "darwin", GOARCH: "amd64"}: "the engine links the same cgo sqlite checkpointer as the " +
			"CLI, so it is excluded from darwin/amd64 for the same reason. The evidence is direct: " +
			"built with CGO_ENABLED=0 it compiles and then fails at runtime, reporting that " +
			"go-sqlite3 requires cgo and that what was linked is a stub",
	},
}

// PlatformsFor returns the platforms a program publishes, in matrix order.
//
// Use this rather than SupportedPlatforms wherever the subject is one
// program's assets: the two differ, and the difference is the point.
func PlatformsFor(program string) []Platform {
	excluded := unpublishable[program]
	platforms := make([]Platform, 0, len(SupportedPlatforms))
	for _, platform := range SupportedPlatforms {
		if _, skip := excluded[platform]; skip {
			continue
		}
		platforms = append(platforms, platform)
	}
	return platforms
}

// ExclusionReason reports why a program does not publish a platform.
func ExclusionReason(program string, platform Platform) (string, bool) {
	reason, ok := unpublishable[program][platform]
	return reason, ok
}

// ArchiveNamesFor renders every asset a program publishes at a version.
func ArchiveNamesFor(program, version string) []string {
	platforms := PlatformsFor(program)
	names := make([]string, 0, len(platforms))
	for _, platform := range platforms {
		names = append(names, ArchiveName(program, platform.GOOS, platform.GOARCH, version))
	}
	return names
}
