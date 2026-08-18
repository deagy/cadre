package cli

import (
	"flag"
	"strings"
)

// flagsFirst reorders args so a FlagSet sees every flag, wherever it was typed.
//
// Go's flag.Parse stops at the first non-flag argument, so
//
//	cadre sbom-check report.spdx.json --min 5
//
// parses zero flags and three positionals. The command then rejects its own
// documented invocation -- `usage: cadre sbom-check <sbom.spdx.json> [--min N]`
// -- which reads as a quoting or path problem rather than an ordering one.
//
// These commands were ported from argparse, which accepts either order, and
// the usage strings came across with them. Nothing caught the difference
// because the only caller that used the documented order was release.yml,
// which had never run.
//
// Rather than reformat every call site to put flags first, accept both. The
// FlagSet is the authority on which flags take a value: a boolean consumes no
// following token, everything else consumes one. Anything after a bare `--`
// is positional by convention and is left alone.
func flagsFirst(fs *flag.FlagSet, args []string) []string {
	var flags, positional []string

	for i := 0; i < len(args); i++ {
		arg := args[i]

		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(arg) < 2 || arg[0] != '-' {
			positional = append(positional, arg)
			continue
		}

		flags = append(flags, arg)
		name := strings.TrimLeft(arg, "-")
		if strings.Contains(name, "=") {
			// --name=value carries its own value.
			continue
		}
		if isBoolFlag(fs, name) {
			continue
		}
		// A value-taking flag consumes the next token, which must travel with
		// it. Leaving it behind would turn `--min 5` into `--min` plus a
		// positional 5, which is a different error than the one being fixed.
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}

	return append(flags, positional...)
}

// isBoolFlag reports whether a registered flag is boolean, using the same
// IsBoolFlag convention the flag package itself uses.
func isBoolFlag(fs *flag.FlagSet, name string) bool {
	registered := fs.Lookup(name)
	if registered == nil {
		return false
	}
	boolean, ok := registered.Value.(interface{ IsBoolFlag() bool })
	return ok && boolean.IsBoolFlag()
}
