package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/deagy/cadre/cli/internal/orchestration"
	"github.com/deagy/cadre/cli/internal/platform"
	"github.com/deagy/cadre/cli/internal/selector"
)

// runSelectGo is the native Go selector, reached only via CADRE_SELECT_IMPL=go.
//
// It is gated by roster/orchestration/test/test_select_differential.py, which
// compares this against the Python implementation over a corpus of input
// shapes and requires byte equality including dispatch_fingerprint. It is not
// the default and does not become the default by passing that gate -- that is
// a separate, deliberate switch.
func runSelectGo(args []string) int {
	options := flag.NewFlagSet("cadre select", flag.ContinueOnError)
	options.SetOutput(os.Stderr)

	task := options.String("task", "", "Task objective used for routing")
	root := options.String("root", "", "Target repository root")
	rosterFlag := options.String("roster", "", "Roster directory")
	base := options.String("base", "", "Git base ref used with <base>...HEAD")
	taskID := options.String("task-id", "", "Stable caller-supplied task identifier")
	classification := options.String("classification", "", "Authorized knowledge classification")
	top := options.Int("top", 5, "Maximum knowledge results per agent")
	format := options.String("format", "json", "json or text")
	output := options.String("output", "", "Write the plan to this path, in the --format chosen")
	explain := options.Bool("explain", false,
		"Additionally print, to stderr, near-miss reasoning for routes that did NOT match this task")
	requireSDLC := options.Bool("require-sdlc", false,
		"Fail instead of degrading to standalone mode if Agentic SDLC isn't available")
	recordTelemetry := options.Bool("record-telemetry", false,
		"Opt in to appending a local, structural-only outcome record to selection-telemetry.jsonl")
	recordTelemetryIncludeTask := options.Bool("record-telemetry-include-task", false,
		"With --record-telemetry, also record the raw task text and changed files")
	telemetryPath := options.String("telemetry-path", "", "Override the telemetry JSON-lines file path")
	var files stringSliceFlag
	options.Var(&files, "files", "Changed path or comma-separated paths; repeatable")
	var sources stringSliceFlag
	options.Var(&sources, "source", "Knowledge-store source to retrieve from; repeatable")

	if err := options.Parse(args); err != nil {
		return 2
	}
	if *task == "" {
		fmt.Fprintln(os.Stderr, "cadre select: error: the following arguments are required: --task")
		return 2
	}
	if *format != "json" && *format != "text" {
		fmt.Fprintf(os.Stderr,
			"cadre select: argument --format: invalid choice: %q (choose from 'json', 'text')\n", *format)
		return 2
	}

	repoRoot, err := FindCadreFile("roster/catalog.yaml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre select: %s\n", err)
		return 1
	}
	suiteRoot := filepath.Dir(filepath.Dir(repoRoot))
	rosterRoot := filepath.Join(suiteRoot, "roster")
	if *rosterFlag != "" {
		rosterRoot = *rosterFlag
	}
	// repository_root is embedded in the plan and therefore in the hashed
	// payload, so it must be the same string Python would produce: expanded,
	// absolutised, and symlink-resolved. `--root .` and `--root $PWD` are the
	// same checkout and must not fingerprint differently.
	targetRoot, err := resolveRepositoryRoot(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre select: %s\n", err)
		return 1
	}

	catalogPath := filepath.Join(rosterRoot, "catalog.yaml")
	routingPath := filepath.Join(rosterRoot, "orchestration", "routing.json")

	catalogRaw, err := os.ReadFile(catalogPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre select: %s\n", err)
		return 1
	}
	catalog, err := selector.ParseCatalogIDs(string(catalogRaw))
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre select: %s\n", err)
		return 1
	}
	routingRaw, err := os.ReadFile(routingPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre select: %s\n", err)
		return 1
	}
	var config map[string]any
	if err := json.Unmarshal(routingRaw, &config); err != nil {
		fmt.Fprintf(os.Stderr, "cadre select: %s: %s\n", routingPath, err)
		return 1
	}

	// A routing overlay changes the effective ruleset, so a plan built from
	// the base rules alone would answer a question the project did not ask.
	//
	// Discovery must match routing_overlay.py exactly: the filename is
	// routing-overlay.json, and it is found by walking up from the repository
	// under selection to the nearest .git boundary -- not by looking only in
	// that repository's own root.
	overlayPath, _ := platform.FindFileAtProjectRoot(
		filepath.Join(selector.OverlayRelativePath...), targetRoot)
	config, err = selector.ResolveEffectiveRouting(config, overlayPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "routing overlay is invalid: %s\n", err)
		return 1
	}

	contract, err := selector.FetchLifecycleContract(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre select: %s\n", err)
		return 1
	}
	var gates []selector.LifecycleGate
	contractVersion := 0
	if contract != nil {
		gates = contract.Gates
		contractVersion = contract.Version
	} else if *requireSDLC {
		// Without --require-sdlc an absent kernel degrades to standalone
		// mode: the plan simply carries no lifecycle applicability. With it,
		// a caller has said that silent degradation is not acceptable for
		// this run, so an absent kernel is an error rather than a quieter
		// plan.
		fmt.Fprintln(os.Stderr, selectInstallMessage(suiteRoot))
		return 1
	}

	// Explicit files answer "route these paths"; a base ref answers "route
	// what this branch changes". Asking both at once is two different
	// questions, and silently preferring one would route on a set the caller
	// did not intend.
	changedFiles := selector.ExplicitFiles(files)
	changedFileSource := "explicit"
	if len(files) > 0 && *base != "" {
		fmt.Fprintln(os.Stderr, "cadre select: --base cannot be combined with --files")
		return 1
	}
	if len(files) == 0 {
		discovered, err := selector.DiscoverChangedFiles(*base, targetRoot)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cadre select: %s\n", err)
			return 1
		}
		changedFiles = discovered.Files
		changedFileSource = discovered.Source
	}

	knowledgeSources := selector.ResolveKnowledgeSources(targetRoot)
	if len(sources) > 0 {
		normalized, err := selector.NormalizeExplicitSources(sources)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cadre select: %s\n", err)
			return 1
		}
		knowledgeSources = normalized
	}

	var provenance map[string]any
	built, err := orchestration.BuildProvenance(catalogPath, routingPath, orchestration.BuildProvenanceOptions{})
	if err == nil && built != nil {
		encoded, marshalErr := json.Marshal(built)
		if marshalErr == nil {
			var decoded map[string]any
			if json.Unmarshal(encoded, &decoded) == nil {
				if contractVersion != 0 {
					decoded["agentic_sdlc_contract_version"] = contractVersion
				}
				provenance = decoded
			}
		}
	}

	plan, err := selector.BuildDispatchPlan(config, selector.PlanInput{
		Task:              *task,
		TaskID:            *taskID,
		RepositoryRoot:    targetRoot,
		Base:              *base,
		ChangedFileSource: changedFileSource,
		ChangedFiles:      changedFiles,
		Classification:    *classification,
		Sources:           knowledgeSources,
		Top:               *top,
	}, selector.PlanOptions{
		Catalog:      catalog,
		Gates:        gates,
		ContractVer:  contractVersion,
		RosterRoot:   rosterRoot,
		KnowledgeCLI: filepath.Join(suiteRoot, "bin", "cadre"),
		Provenance:   provenance,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		return 1
	}

	if selector.TelemetryIsEnabled(*recordTelemetry) {
		if _, err := selector.RecordSelection(plan, targetRoot, *telemetryPath,
			selector.TelemetryIncludeTask(*recordTelemetryIncludeTask)); err != nil {
			fmt.Fprintf(os.Stderr, "cadre select: %s\n", err)
			return 1
		}
	}

	var rendered []byte
	if *format == "text" {
		rendered = []byte(selector.FormatPlanText(plan))
	} else {
		rendered, err = selector.RenderPlanJSON(plan)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cadre select: %s\n", err)
			return 1
		}
	}

	if *output != "" {
		destination, err := filepath.Abs(*output)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cadre select: %s\n", err)
			return 1
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "cadre select: %s\n", err)
			return 1
		}
		if err := os.WriteFile(destination, rendered, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "cadre select: %s\n", err)
			return 1
		}
	} else {
		_, _ = os.Stdout.Write(rendered)
	}

	if *explain {
		// Printed to stderr, after the machine-readable plan, and derived
		// only from data the plan already exposes plus the effective routing
		// config. It never touches the plan or the rendered bytes, so the
		// output is byte-identical with and without --explain.
		nearMisses := selector.FindNearMisses(config, *task, selector.MatchedRouteIDs(plan))
		fmt.Fprint(os.Stderr, selector.FormatNearMissesText(nearMisses))
	}
	return 0
}

// resolveRepositoryRoot mirrors Path(root).expanduser().resolve(), defaulting
// to the working directory, and refuses a path that is not a directory.
func resolveRepositoryRoot(root string) (string, error) {
	if root == "" {
		working, err := os.Getwd()
		if err != nil {
			return "", err
		}
		root = working
	} else if root == "~" || strings.HasPrefix(root, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(root, "~"), "/"))
	}

	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	// EvalSymlinks is the .resolve() half that matters: a checkout reached
	// through a symlink must produce the same plan as one reached directly.
	// It fails on a path that does not exist, which the directory check below
	// reports in the caller's own terms.
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		absolute = resolved
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("Repository root is not a directory: %s", absolute) //nolint:staticcheck // ported message
	}
	return absolute, nil
}

// selectInstallMessage is agentic_sdlc_contracts.install_message().
//
// Deliberately not SDLCInstallMessage: that one mirrors bin/cadre.py's
// sdlc_install_message, a different function with different wording for a
// different command. This one names AGENTIC_SDLC_BIN, because that is the
// setting a `cadre select --require-sdlc` caller would reach for.
//
// The version range is read from provider.json rather than written down.
// That file carries two unrelated version lines -- its own manifest version
// and kernel_compatibility -- and every install message in this repository
// once quoted the former while meaning the latter, sending operators to a
// kernel ten minor versions too old.
func selectInstallMessage(suiteRoot string) string {
	requirement := "a compatible version"
	if compatibility, err := readKernelCompatibility(suiteRoot); err == nil {
		requirement = fmt.Sprintf("v%s or newer (below v%s)",
			compatibility.Minimum, compatibility.MaximumExclusive)
	}
	return fmt.Sprintf(
		"Agentic SDLC %s is required; set AGENTIC_SDLC_BIN or install https://github.com/deagy/cadre",
		requirement)
}
