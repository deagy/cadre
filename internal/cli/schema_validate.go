package cli

import (
	"flag"
	"fmt"
	"os"

	"github.com/deagy/cadre/cli/internal/orchestration"
	"github.com/deagy/cadre/cli/internal/platform"
)

// SchemaValidateCmd is the `cadre schema-validate` command: strict,
// standalone JSON Schema validation for roster/catalog.yaml and
// roster/orchestration/routing.json (plus roster/roster.json, when
// present). Prints every finding and exits non-zero if either file is
// invalid; prints a one-line summary and exits zero when both are clean.
func SchemaValidateCmd(args []string) int {
	fs := flag.NewFlagSet("cadre schema-validate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	catalogFlag := fs.String("catalog", "", "Path to catalog.yaml (default: <repo>/roster/catalog.yaml)")
	routingFlag := fs.String("routing", "", "Path to routing.json (default: <repo>/roster/orchestration/routing.json)")
	catalogSchemaFlag := fs.String("catalog-schema", "", "Path to catalog.schema.json (default: <repo>/roster/catalog.schema.json)")
	routingSchemaFlag := fs.String("routing-schema", "", "Path to routing.schema.json (default: <repo>/roster/orchestration/routing.schema.json)")
	rosterManifestFlag := fs.String("roster-manifest", "", "Path to roster.json (default: <repo>/roster/roster.json)")
	rosterSchemaFlag := fs.String("roster-schema", "", "Path to roster.schema.json (default: <repo>/roster/orchestration/roster.schema.json)")
	agentsRootFlag := fs.String("agents-root", "", "Base directory catalog.yaml's definition fields resolve against (default: --catalog's parent)")

	if err := fs.Parse(args); err != nil {
		return parseExitCode(err)
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "cadre schema-validate: unexpected argument: %s\n", fs.Arg(0))
		return 2
	}

	repoRoot, err := platform.FindInstallationRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre: cannot find installation root: %v\n", err)
		return 1
	}

	paths := orchestration.DefaultSchemaValidationPaths(repoRoot)
	if *catalogFlag != "" {
		paths.CatalogPath = *catalogFlag
	}
	if *routingFlag != "" {
		paths.RoutingPath = *routingFlag
	}
	if *catalogSchemaFlag != "" {
		paths.CatalogSchemaPath = *catalogSchemaFlag
	}
	if *routingSchemaFlag != "" {
		paths.RoutingSchemaPath = *routingSchemaFlag
	}
	if *rosterManifestFlag != "" {
		paths.RosterManifestPath = *rosterManifestFlag
	}
	if *rosterSchemaFlag != "" {
		paths.RosterSchemaPath = *rosterSchemaFlag
	}
	if *agentsRootFlag != "" {
		paths.AgentsRoot = *agentsRootFlag
	}

	findings, err := orchestration.RunSchemaValidation(paths)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre schema-validate: %v\n", err)
		return 1
	}
	if len(findings) > 0 {
		for _, f := range findings {
			fmt.Fprintln(os.Stderr, f)
		}
		return 1
	}

	// Names every document actually checked. A success line that
	// under-reports its own scope is how a validator quietly stops
	// covering something.
	validated := []string{paths.CatalogPath, paths.RoutingPath}
	if info, err := os.Stat(paths.RosterManifestPath); err == nil && !info.IsDir() {
		validated = append(validated, paths.RosterManifestPath)
	}
	fmt.Print("schema validation passed: ")
	for i, v := range validated {
		if i > 0 {
			fmt.Print(", ")
		}
		fmt.Print(v)
	}
	fmt.Println(" are schema-valid")
	return 0
}
