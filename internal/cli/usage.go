package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// The argument grammar each subcommand shows after its own name.
//
// Held here, once, because every one of these strings has two call sites --
// the `--help` block built by setUsage below, and the bare `usage: ...` line
// the command prints when it rejects its arguments. They were previously only
// the second of those, so `--help` fell through to Go's default and the two
// could not even disagree: one of them did not exist.
const (
	usageBootstrapCodex        = "[--source DIR] [--target DIR]"
	usageContext               = "<init|put|get|list|search|reindex|export|promote|prune-audit|drop|expire|stats> [options]"
	usageGenerateAuthorityAide = "[--check]"
	usageGenerateRoleMetadata  = "[--check]"
	usageInit                  = "[TARGET] [--target DIR] [--answers FILE] [--set [REGION:]PATH=VALUE ...] [--stack ID] [--sections LIST] [--dry-run] [--force] [--repair [--apply]] [--print-answers] [--interactive]"
	usageResolveShared         = "<filename> [--project <dir>]"
	usageRoleFidelity          = "[--mode static|probe] [options]"
	usageSelectionTelemetry    = "--summarize FILE"
)

// setUsage gives fs a `--help` block opening with the command's public name,
// in the exact `usage: cadre <name> ...` form that
// roster/orchestration/test/test_cli_surface.py requires of every subcommand
// in bin/subcommands.tsv.
//
// Without it, Go's flag package prints its own `Usage of cadre <name>:`
// header -- which names the flag set rather than the command the user typed,
// and reads as a different program from the argparse-era `usage: cadre
// <name>` these subcommands printed before the Python-to-Go migration. Eight
// subcommands were still on that default. The drift was invisible for as long
// as bin/subcommands.tsv was empty, because the guard iterates that file and
// an empty one vacated every subtest rather than failing.
//
// synopsis is the argument grammar shown after the name; details are optional
// paragraphs printed between the usage line and the flag defaults. Output
// goes to stderr, matching where argparse put a usage block and where the
// existing argument-rejection paths already write.
func setUsage(fs *flag.FlagSet, name, synopsis string, details ...string) {
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		out := fs.Output()
		line := "usage: cadre " + name
		if synopsis != "" {
			line += " " + synopsis
		}
		_, _ = fmt.Fprintln(out, line)
		for _, paragraph := range details {
			_, _ = fmt.Fprintf(out, "\n%s\n", strings.TrimRight(paragraph, "\n"))
		}
		if hasFlags(fs) {
			_, _ = fmt.Fprintln(out, "\nOptions:")
			fs.PrintDefaults()
		}
	}
}

// hasFlags reports whether fs defines any flag at all, so a flagless command
// does not print an empty "Options:" heading.
func hasFlags(fs *flag.FlagSet) bool {
	found := false
	fs.VisitAll(func(*flag.Flag) { found = true })
	return found
}
