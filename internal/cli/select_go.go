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
	// Fail closed rather than silently answering a different question. These
	// flags exist in the Python surface; until this implementation reproduces
	// them, saying so is the only honest response -- a plan that quietly
	// ignored --explain or wrote no --output file would be wrong in a way the
	// caller could not see.
	if *format != "json" {
		fmt.Fprintf(os.Stderr, "cadre select: --format=%s is not implemented by the Go selector yet; unset %s\n",
			*format, SelectImplEnv)
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
	targetRoot := *root
	if targetRoot == "" {
		if working, err := os.Getwd(); err == nil {
			targetRoot = working
		}
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

	// A routing overlay changes the effective ruleset, so ignoring one would
	// silently produce a plan for different rules than the project declared.
	// Refuse instead, until the overlay merge is ported.
	//
	// Discovery must match routing_overlay.py exactly: the filename is
	// routing-overlay.json, and it is found by walking up from the repository
	// under selection to the nearest .git boundary -- not by looking only in
	// that repository's own root. A guard that checked one fixed path would
	// miss a real overlay and then proceed, which is precisely the outcome
	// this refusal exists to prevent.
	overlayPath, hasOverlay := platform.FindFileAtProjectRoot(
		filepath.Join(".agents", "orchestration", "routing-overlay.json"), targetRoot)
	if hasOverlay {
		fmt.Fprintf(os.Stderr,
			"cadre select: a routing overlay exists at %s and the Go selector does not apply overlays yet; unset %s\n",
			overlayPath, SelectImplEnv)
		return 2
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
	}

	changedFiles := splitChangedFiles(files)
	changedFileSource := "explicit"
	if len(changedFiles) == 0 {
		fmt.Fprintln(os.Stderr,
			"cadre select: the Go selector requires --files; git-status discovery is not ported yet")
		return 2
	}

	knowledgeSources := []string(sources)
	if len(knowledgeSources) == 0 {
		fmt.Fprintln(os.Stderr,
			"cadre select: the Go selector requires --source; origin-derived defaults are not ported yet")
		return 2
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

	rendered, err := selector.RenderPlanJSON(plan)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre select: %s\n", err)
		return 1
	}
	_, _ = os.Stdout.Write(rendered)
	return 0
}

// splitChangedFiles flattens repeatable --files values, each of which may
// itself be comma-separated.
func splitChangedFiles(values []string) []string {
	var out []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}
