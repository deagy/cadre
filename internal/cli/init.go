package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/deagy/cadre/cli/internal/initproject"
	"github.com/deagy/cadre/cli/internal/platform"
)

// InitCmd is the `cadre init` command: init_project.py's non-interactive
// surface (--answers, --set, --stack, defaults-mode, --dry-run/--force,
// --repair, --print-answers). See internal/initproject's package doc for
// what is and is not ported (the --interactive questionnaire is not).
func InitCmd(args []string) int {
	fs := flag.NewFlagSet("cadre init", flag.ContinueOnError)
	setUsage(fs, "init", usageInit)

	target := fs.String("target", "", "Project root (legacy spelling; cannot be combined with TARGET)")
	stack := fs.String("stack", "", "Named starter preset id from roster/shared/init-presets/*.yaml")
	answers := fs.String("answers", "", "Non-interactive answer file (schema_version: 1)")
	interactive := fs.Bool("interactive", false, "Start the overlay questionnaire")
	sections := fs.String("sections", "", "Comma-separated subset of rg-a-stack,rg-b-governance,rg-c-platform (default: all)")
	dryRun := fs.Bool("dry-run", false, "Preview only (default unless --force)")
	force := fs.Bool("force", false, "Required to actually write")
	repair := fs.Bool("repair", false, "Inspect existing shared-policy overlays for stale or invalid state; always read-only")
	apply := fs.Bool("apply", false, "Acknowledge the repair inspection (valid only with --repair; no automatic overlay rewrite)")
	printAnswers := fs.Bool("print-answers", false, "Echo the resolved answer set after validation, redacted")
	var setValues stringSliceFlag
	fs.Var(&setValues, "set", "Override one shipped-default field without an answer file, repeatable ([REGION:]PATH=VALUE)")

	// argparse allows TARGET to appear anywhere among the optional flags
	// (e.g. `cadre init --force TARGET` or `cadre init TARGET --force`);
	// Go's flag.Parse stops consuming flags at the first non-flag argument,
	// so pull TARGET out first and feed flag.Parse only the flag tokens.
	positional, flagArgs, err := extractPositional(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cadre init: "+err.Error())
		return 2
	}
	if err := fs.Parse(flagArgs); err != nil {
		return parseExitCode(err)
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "usage: cadre init "+usageInit)
		return 2
	}

	installationRoot, err := platform.FindInstallationRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre init: cannot find installation root: %v\n", err)
		return 1
	}
	sharedDefaultsDir := filepath.Join(installationRoot, "roster", "shared")

	opts := initproject.RunInitOptions{
		TargetPath:        positional,
		Target:            *target,
		Stack:             *stack,
		AnswersPath:       *answers,
		Interactive:       *interactive,
		SetValues:         []string(setValues),
		Sections:          *sections,
		DryRun:            *dryRun,
		Force:             *force,
		Repair:            *repair,
		Apply:             *apply,
		PrintAnswers:      *printAnswers,
		SharedDefaultsDir: sharedDefaultsDir,
	}
	return initproject.RunInit(opts, os.Stdout, os.Stderr)
}

// initBoolFlags/initValueFlags list InitCmd's flag names, so
// extractPositional can tell a flag needing a following value from a bare
// boolean flag when scanning for the interspersed TARGET positional.
var (
	initBoolFlags  = map[string]bool{"interactive": true, "dry-run": true, "force": true, "repair": true, "apply": true, "print-answers": true}
	initValueFlags = map[string]bool{"target": true, "stack": true, "answers": true, "sections": true, "set": true}
)

// extractPositional pulls the single optional TARGET positional argument out
// of args (wherever it appears), returning it separately from the remaining
// flag tokens in their original relative order, so those can be handed to
// flag.Parse without it stopping early at a non-flag argument.
func extractPositional(args []string) (positional string, flagArgs []string, err error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		// Any leading dash marks a flag token, single or double. Matching
		// only "--" left `-h` to fall through as TARGET, so `cadre init -h`
		// meant "initialise a project in a directory named -h" -- it got as
		// far as refusing to write into this checkout, exit 1, with no hint
		// that a flag had been misread. The same applied to every
		// single-dash spelling Go's flag package accepts.
		if len(arg) >= 2 && arg[0] == '-' {
			name := strings.TrimLeft(arg, "-")
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				flagArgs = append(flagArgs, arg)
				continue
			}
			flagArgs = append(flagArgs, arg)
			if initValueFlags[name] && !initBoolFlags[name] {
				if i+1 < len(args) {
					i++
					flagArgs = append(flagArgs, args[i])
				}
			}
			continue
		}
		if positional != "" {
			return "", nil, fmt.Errorf("provide either TARGET or --target, not both, and only one TARGET")
		}
		positional = arg
	}
	return positional, flagArgs, nil
}

// stringSliceFlag implements flag.Value for a repeatable --set-style flag.
type stringSliceFlag []string

func (s *stringSliceFlag) String() string {
	if s == nil {
		return ""
	}
	return fmt.Sprintf("%v", []string(*s))
}

func (s *stringSliceFlag) Set(value string) error {
	*s = append(*s, value)
	return nil
}
