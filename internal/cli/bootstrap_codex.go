package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/deagy/cadre/cli/internal/orchestration"
	"github.com/deagy/cadre/cli/internal/platform"
)

// BootstrapCodexCmd is the `cadre bootstrap-codex` command: a faithful port
// of sync_codex_agents.py, installing this suite's namespaced Codex role
// wrappers into a Codex agents directory without touching bare role files
// there that this suite does not own.
func BootstrapCodexCmd(args []string) int {
	fs := flag.NewFlagSet("cadre bootstrap-codex", flag.ContinueOnError)
	setUsage(fs, "bootstrap-codex", usageBootstrapCodex)
	source := fs.String("source", "", "defaults to <repo root>/provider/codex-agents")
	target := fs.String("target", "", "defaults to ~/.codex/agents")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "usage: cadre bootstrap-codex "+usageBootstrapCodex)
		return 2
	}

	resolvedSource := *source
	if resolvedSource == "" {
		installationRoot, err := platform.FindInstallationRoot()
		if err != nil {
			fmt.Fprintf(os.Stderr, "agents: cannot find installation root: %v\n", err)
			return 1
		}
		resolvedSource = filepath.Join(installationRoot, "provider", "codex-agents")
	}
	absSource, err := filepath.Abs(resolvedSource)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agents: %v\n", err)
		return 1
	}

	resolvedTarget := *target
	if resolvedTarget == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "agents: %v\n", err)
			return 1
		}
		resolvedTarget = filepath.Join(home, ".codex", "agents")
	} else if len(resolvedTarget) >= 1 && resolvedTarget[0] == '~' {
		expanded, err := expandUserPath(resolvedTarget)
		if err != nil {
			fmt.Fprintf(os.Stderr, "agents: %v\n", err)
			return 1
		}
		resolvedTarget = expanded
	}
	absTarget, err := filepath.Abs(resolvedTarget)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agents: %v\n", err)
		return 1
	}

	result, err := orchestration.SyncWrappers(absSource, absTarget)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agents: %v\n", err)
		return 1
	}
	fmt.Printf("Installed %d; unchanged %d. Index %s at %s.\n",
		len(result.Installed), len(result.Unchanged), result.IndexStatus, result.IndexPath)
	return 0
}

func expandUserPath(path string) (string, error) {
	if path == "~" {
		return os.UserHomeDir()
	}
	if len(path) >= 2 && path[0] == '~' && (path[1] == '/' || path[1] == filepath.Separator) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}
