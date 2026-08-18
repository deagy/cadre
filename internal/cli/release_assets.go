package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/deagy/cadre/cli/internal/release"
)

// ReleaseAssetsCmd prints the archive names a program publishes at a version.
//
// It exists so the release workflow can verify its own output against the
// contract rather than against a list retyped into YAML. There were three such
// lists -- an inline Python set in cli-publish, a shell loop in kernel-publish,
// and internal/release.SupportedPlatforms -- and only the last one was ever
// tested. The comment on the Python one asserted "Five platforms", which
// stayed true only for as long as nobody changed the matrix.
//
// The lists are also no longer interchangeable: the CLI cannot publish
// darwin/amd64 (cgo needs a native Intel macOS runner, and GitHub retired the
// free one) while the kernel still can, so a single hardcoded set is now wrong
// for one of the two jobs no matter which set is chosen.
func ReleaseAssetsCmd(args []string) int {
	fs := flag.NewFlagSet("cadre release-assets", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: cadre release-assets --program NAME --version X.Y.Z

Print, one per line, every archive a program publishes at a version.

Options:
`)
		fs.PrintDefaults()
	}
	program := fs.String("program", "", "Program to list assets for ("+strings.Join(release.Programs, " or ")+")")
	version := fs.String("version", "", "Version, without a leading v")
	explain := fs.Bool("explain", false, "Also print, to stderr, any platform this program does not publish and why")

	if err := fs.Parse(args); err != nil {
		return parseExitCode(err)
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "cadre release-assets: unexpected argument: %s\n", fs.Arg(0))
		return 2
	}

	known := false
	for _, name := range release.Programs {
		if *program == name {
			known = true
			break
		}
	}
	if !known {
		fmt.Fprintf(os.Stderr, "cadre release-assets: --program must be one of %s (got %q)\n",
			strings.Join(release.Programs, ", "), *program)
		return 2
	}
	if strings.TrimSpace(*version) == "" {
		fmt.Fprintln(os.Stderr, "cadre release-assets: --version is required")
		return 2
	}
	if strings.HasPrefix(*version, "v") {
		fmt.Fprintf(os.Stderr, "cadre release-assets: --version takes a bare version, not %q\n", *version)
		return 2
	}

	for _, name := range release.ArchiveNamesFor(*program, *version) {
		fmt.Println(name)
	}

	if *explain {
		for _, platform := range release.SupportedPlatforms {
			if reason, excluded := release.ExclusionReason(*program, platform); excluded {
				fmt.Fprintf(os.Stderr, "%s does not publish %s: %s\n", *program, platform, reason)
			}
		}
	}
	return 0
}
