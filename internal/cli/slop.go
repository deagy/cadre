package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"

	"github.com/deagy/cadre/cli/internal/config"
	"github.com/deagy/cadre/cli/internal/slop"
)

const (
	usageSlop = "scan [options] <path|glob|-> ... | scan --git-range A..B"

	slopRulesFilename     = "slop-rules.yaml"
	slopRulesRelativePath = "roster/shared/" + slopRulesFilename
)

// SlopCmd implements `cadre slop`. Only `scan` exists in this release.
func SlopCmd(args []string) int {
	return runSlop(context.Background(), args, os.Stdin, os.Stdout, os.Stderr)
}

func runSlop(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		writeSlopUsage(stderr)
		return 2
	}
	if isHelpArg(args[0]) {
		writeSlopUsage(stderr)
		return 0
	}
	switch args[0] {
	case "scan":
		return runSlopScan(ctx, args[1:], stdin, stdout, stderr)
	case "fix", "baseline":
		fmt.Fprintf(stderr, "cadre slop: %q is planned and not available in this release; only `scan` exists\n", args[0])
		return 2
	}
	fmt.Fprintf(stderr, "cadre slop: unknown verb %q\n", args[0])
	writeSlopUsage(stderr)
	return 2
}

func writeSlopUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: cadre slop "+usageSlop)
	fmt.Fprintln(w, "\nVerbs:")
	fmt.Fprintln(w, "  scan   Find AI-slop tells in prose: deterministic, offline, no model calls")
	fmt.Fprintln(w, "\nRun `cadre slop scan --help` for the options.")
}

func runSlopScan(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cadre slop scan", flag.ContinueOnError)
	setUsage(fs, "slop scan", "[options] <path|glob|-> ... | --git-range A..B",
		"Scan text for machine-writing tells: em dashes, \"not X, it's Y\" reversals, recap "+
			"endings, filler vocabulary, sycophancy. Deterministic and offline.\n\n"+
			"Inputs: files, directories (prose files only), globs, or - for stdin. "+
			"--git-range scans each commit message in the range and reports hits with the commit SHA.\n\n"+
			"Fenced code, inline code, link targets and quoted spans are never scanned, so a document "+
			"may quote a tell as an exhibit. `slop-ok: <reason>` anywhere in a piece exempts it; "+
			"the reason is mandatory and is listed in the report.\n\n"+
			"A repository may disable a rule by id, with a reason, in .agents/shared/slop-rules.yaml "+
			"(`disabled:` list of id and reason). Disabled rules appear in every report.\n\n"+
			"Exit codes: 0 clear, 1 tells found, 2 usage, I/O or rules-file error.\n\n"+
			"Limit: regular expressions cannot see structural slop (invented frameworks, "+
			"restatement, uniform rhythm). A reviewer still has to read the text.")
	fs.SetOutput(stderr)
	format := fs.String("format", "text", "output format: text or json")
	surfaceName := fs.String("surface", "markdown", "how to read each input: markdown or commit")
	gitRange := fs.String("git-range", "", "scan the commit messages in a git range such as origin/main..HEAD")
	limit := fs.Int("counted-limit", 0, "counted tells that fail a piece (default: the rules file's counted_limit)")
	project := fs.String("project", ".", "directory to start the search for .agents/shared/slop-rules.yaml")
	if err := fs.Parse(flagsFirst(fs, args)); err != nil {
		return parseExitCode(err)
	}

	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "cadre slop scan: "+format+"\n", a...)
		return 2
	}
	if *format != "text" && *format != "json" {
		return fail("--format must be text or json, got %q", *format)
	}
	surface, ok := slop.ParseSurface(*surfaceName)
	if !ok {
		return fail("--surface must be markdown or commit, got %q", *surfaceName)
	}
	if *limit < 0 {
		return fail("--counted-limit must not be negative")
	}
	if fs.NArg() == 0 && *gitRange == "" {
		fmt.Fprintln(stderr, "usage: cadre slop "+usageSlop)
		return 2
	}

	set, disabled, err := loadSlopRules(*project)
	if err != nil {
		return fail("%v", err)
	}

	units, skipped, err := slop.ReadUnits(fs.Args(), surface, stdin)
	if err != nil {
		return fail("%v", err)
	}
	if *gitRange != "" {
		commits, err := slop.CommitUnits(ctx, ".", *gitRange)
		if err != nil {
			return fail("%v", err)
		}
		units = append(units, commits...)
	}

	report, err := slop.Scan(set, units, slop.Options{Disabled: disabled, CountedLimit: *limit})
	if err != nil {
		return fail("%v", err)
	}
	if *format == "json" {
		err = slop.WriteJSON(stdout, report, skipped)
	} else {
		err = slop.WriteText(stdout, report, skipped)
	}
	if err != nil {
		return fail("writing report: %v", err)
	}
	if report.Failed {
		return 1
	}
	return 0
}

// loadSlopRules reads the shipped rules and the project's disabled list.
//
// The rules come from roster/shared/slop-rules.yaml, located the way every
// other roster/shared file is (CADRE_REPO_ROOT, the binary's own checkout or
// plugin, then the working directory). There is no built-in copy to fall
// back on: a missing or malformed file is an error, so a broken install can
// never report "clear".
//
// The project overlay is resolved by config.ResolveSharedConfig, which brings
// its symlink-escape guard. Only the `disabled` key may differ from the
// shipped file; custom rule lists are not supported.
func loadSlopRules(project string) (*slop.RuleSet, []slop.Disabled, error) {
	path, err := FindCadreFile(slopRulesRelativePath)
	if err != nil {
		return nil, nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", path, err)
	}
	set, err := slop.ParseRules(data)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}

	base, err := config.LoadStructured(path)
	if err != nil {
		return nil, nil, err
	}
	resolved, err := config.ResolveSharedConfig(dirOf(path), slopRulesFilename, project)
	if err != nil {
		return nil, nil, err
	}
	for key, value := range resolved.Structured {
		if key == "disabled" {
			continue
		}
		if !reflect.DeepEqual(value, base[key]) {
			return nil, nil, fmt.Errorf(
				"the %s overlay changes %q; only a `disabled` list is supported (custom rule lists are not)",
				slopRulesFilename, key)
		}
	}
	disabled, err := parseDisabled(resolved.Structured["disabled"])
	if err != nil {
		return nil, nil, err
	}
	return set, disabled, nil
}

func dirOf(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[:i]
	}
	return "."
}

func parseDisabled(value any) ([]slop.Disabled, error) {
	if value == nil {
		return nil, nil
	}
	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: `disabled` must be a list of {id, reason}", slopRulesFilename)
	}
	var out []slop.Disabled
	for index, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: disabled[%d] must be a mapping with id and reason", slopRulesFilename, index)
		}
		id, idOK := entry["id"].(string)
		reason, reasonOK := entry["reason"].(string)
		if !idOK || !reasonOK || len(entry) != 2 || strings.TrimSpace(id) == "" || strings.TrimSpace(reason) == "" {
			return nil, fmt.Errorf("%s: disabled[%d] needs exactly a non-empty id and a non-empty reason", slopRulesFilename, index)
		}
		out = append(out, slop.Disabled{ID: id, Reason: reason})
	}
	return out, nil
}
