package cli

import (
	"errors"
	"flag"
)

// parseExitCode maps a flag-parsing failure to a process exit code.
//
// A FlagSet built with flag.ContinueOnError reports an explicit -h or --help
// as flag.ErrHelp, having already printed usage. That is a satisfied request,
// not a usage error, and the two need different exit codes: help is 0,
// everything else -- an unknown flag, a malformed value -- is 2.
//
// Collapsing them made `cadre select --help`, `knowledge --help`, `config
// --help`, `init --help` and `profile --help` print correct help and exit 2,
// while `cadre --help`, `doctor --help` and `sdlc --help` exited 0. Any
// caller running under `set -e` breaks on the first group, which is how this
// surfaced: the release workflow's smoke test runs `cadre knowledge --help`,
// and the first release that ever reached that step failed on it.
func parseExitCode(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	return 2
}

// parseFailed reports whether a flag-parsing error is a real usage error, as
// opposed to a satisfied -h/--help request. Call sites that print the error
// before exiting need this: printing "cadre: flag: help requested" under a
// correctly rendered usage block is noise.
func parseFailed(err error) bool {
	return !errors.Is(err, flag.ErrHelp)
}

// isHelpArg reports whether an argument is an explicit request for usage.
//
// Subcommands that dispatch on args[0] rather than through a FlagSet never
// reach flag.ErrHelp: to them `--help` is simply an unrecognised verb, so
// they print usage and exit 2. `cadre config --help` and `cadre profile
// --help` both did.
//
// Usage still goes to stderr here, matching every other command in the CLI.
// Sending an explicit help request to stdout would be the better convention,
// but it is a separate change and applies to all of them at once.
func isHelpArg(arg string) bool {
	switch arg {
	case "-h", "--help", "help":
		return true
	}
	return false
}
