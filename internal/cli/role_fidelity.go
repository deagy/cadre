package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/deagy/cadre/cli/internal/orchestration"
	"github.com/deagy/cadre/cli/internal/platform"
)

// RoleFidelityCmd is the `cadre role-fidelity` command: measures whether a
// role's payload survives contact with a given model, in two independent
// modes -- static (context-budget arithmetic, no model, no network) and
// probe (live scoring against an OpenAI-compatible endpoint).
func RoleFidelityCmd(args []string) int {
	fs := flag.NewFlagSet("cadre role-fidelity", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	mode := fs.String("mode", "static", "static or probe")
	presetsDir := fs.String("presets-dir", "", "Directory of *.md role presets")
	var roles multiFlag
	fs.Var(&roles, "role", "Restrict to a role (repeatable)")
	asJSON := fs.Bool("json", false, "Emit the full JSON report")
	output := fs.String("output", "", "Write the JSON report to a file")

	contextBudget := fs.Int("context-budget", 8192, "Model context window in tokens")
	reserve := fs.Int("reserve", 2048, "Tokens reserved for task, retrieved context, tool schemas and reply")
	charsPerToken := fs.Float64("chars-per-token", orchestration.DefaultCharsPerToken, "Chars-per-token estimate divisor")

	probesFlag := fs.String("probes", "", "Probe file (default: role-fidelity-probes.yaml next to routing.json)")
	baseURL := fs.String("base-url", os.Getenv("CADRE_FIDELITY_BASE_URL"), "OpenAI-compatible endpoint base URL")
	model := fs.String("model", os.Getenv("CADRE_FIDELITY_MODEL"), "Model name")
	apiKey := fs.String("api-key", os.Getenv("CADRE_FIDELITY_API_KEY"), "API key, if the endpoint needs one")
	temperature := fs.Float64("temperature", 0.0, "Sampling temperature")
	maxTokensFlag := fs.Int("max-tokens", 0, "Max reply tokens (0 = unset)")
	timeoutSeconds := fs.Float64("timeout", 120.0, "Request timeout in seconds")
	dryRun := fs.Bool("dry-run", false, "Show what would be sent; send nothing")
	compareCondensed := fs.Bool("compare-condensed", false, "Run every probe against both a full and a condensed brief")
	noWarnDegenerate := fs.Bool("no-warn-degenerate", false, "Skip flagging probes whose keywords already appear in the brief")
	failUnder := fs.Float64("fail-under", -1, "Exit non-zero if the pass rate falls below this (0..1)")
	minCoverage := fs.Float64("min-coverage", -1, "Exit non-zero if coverage falls below this (0..1)")
	attestFile := fs.String("attest-file", "", "Write a role-fidelity attestation record for this run's --model into this JSON file")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "cadre role-fidelity: unexpected argument: %s\n", fs.Arg(0))
		return 2
	}

	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre: cannot get working directory: %v\n", err)
		return 1
	}
	repoRoot, err := platform.FindProjectRoot(wd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre: cannot find repository root: %v\n", err)
		return 1
	}

	dir := *presetsDir
	if dir == "" {
		dir, err = orchestration.DefaultFidelityPresetsDir(repoRoot)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cadre role-fidelity: %v\n", err)
			return 2
		}
	}
	tierMap := orchestration.TierNormalizationMap(orchestration.DefaultRunnerCapabilitiesPath(repoRoot))
	presets, err := orchestration.LoadFidelityPresets(dir, roles, tierMap)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre role-fidelity: %v\n", err)
		return 2
	}

	var reportJSON any
	var rendered string
	var attestExportLine string

	if *mode == "static" {
		report := orchestration.StaticFidelityReportFor(presets, *contextBudget, *reserve, *charsPerToken)
		reportJSON = report
		rendered = renderStaticFidelity(report)

		if err := emitFidelityOutput(reportJSON, rendered, *asJSON, *output); err != nil {
			fmt.Fprintf(os.Stderr, "cadre role-fidelity: %v\n", err)
			return 1
		}
		if report.OverBudgetCount > 0 {
			return 1
		}
		return 0
	}

	// Probe mode.
	probesPath := *probesFlag
	if probesPath == "" {
		probesPath = repoRoot + "/roster/orchestration/src/" + orchestration.DefaultProbesFilename
	}
	if info, statErr := os.Stat(probesPath); statErr != nil || info.IsDir() {
		fmt.Fprintf(os.Stderr, "cadre role-fidelity: %s: probe file not found\n", probesPath)
		return 2
	}
	probes, err := orchestration.LoadFidelityProbes(probesPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre role-fidelity: %v\n", err)
		return 2
	}

	var client *orchestration.FidelityChatClient
	if !*dryRun {
		if *baseURL == "" || *model == "" {
			fmt.Fprintln(os.Stderr, "cadre role-fidelity: probe mode needs --base-url and --model (or "+
				"CADRE_FIDELITY_BASE_URL / CADRE_FIDELITY_MODEL). Use --dry-run to inspect the run without "+
				"sending anything. For a local Ollama, --base-url http://localhost:11434/v1")
			return 2
		}
		client = &orchestration.FidelityChatClient{
			BaseURL:     *baseURL,
			Model:       *model,
			APIKey:      *apiKey,
			Temperature: *temperature,
			Timeout:     secondsToDuration(*timeoutSeconds),
		}
		if *maxTokensFlag > 0 {
			mt := *maxTokensFlag
			client.MaxTokens = &mt
		}
	}
	warnDegenerate := !*noWarnDegenerate

	var passRate, coverage *float64
	if *compareCondensed {
		report := orchestration.RunFidelityCondensedComparison(presets, probes, client, warnDegenerate)
		reportJSON = report
		rendered = renderCondensedFidelityComparison(report)
		passRate, coverage = report.PassRate, report.Coverage
	} else {
		report := orchestration.RunFidelityProbes(presets, probes, client, warnDegenerate)
		reportJSON = report
		rendered = renderProbeFidelity(report)
		passRate, coverage = report.PassRate, report.Coverage

		if *attestFile != "" {
			merged, err := orchestration.WriteFidelityAttestation(report, *attestFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "cadre role-fidelity: %v\n", err)
				return 2
			}
			mergedJSON, _ := json.Marshal(merged)
			attestExportLine = fmt.Sprintf("export %s=%s", orchestration.CLINEAttestationEnv, shellQuote(string(mergedJSON)))
		}
	}

	if err := emitFidelityOutput(reportJSON, rendered, *asJSON, *output); err != nil {
		fmt.Fprintf(os.Stderr, "cadre role-fidelity: %v\n", err)
		return 1
	}
	if attestExportLine != "" {
		fmt.Printf("\nAttestation for model %q written to %s. To silence the cline-agents no-attestation "+
			"notice for this model, export it before dispatch:\n\n%s\n", *model, *attestFile, attestExportLine)
	}

	if !*dryRun {
		if *failUnder >= 0 && valueOr(passRate, 0.0) < *failUnder {
			return 1
		}
		if *minCoverage >= 0 && valueOr(coverage, 0.0) < *minCoverage {
			return 1
		}
	}
	return 0
}

func emitFidelityOutput(reportJSON any, rendered string, asJSON bool, outputPath string) error {
	if outputPath != "" {
		data, err := json.MarshalIndent(reportJSON, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(outputPath, data, 0o644); err != nil {
			return err
		}
	}
	if asJSON {
		data, err := json.MarshalIndent(reportJSON, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
	} else {
		fmt.Println(rendered)
		if outputPath != "" {
			fmt.Printf("\nFull report written to %s\n", outputPath)
		}
	}
	return nil
}

func valueOr(v *float64, fallback float64) float64 {
	if v == nil {
		return fallback
	}
	return *v
}

func secondsToDuration(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
}

// multiFlag implements flag.Value for a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
