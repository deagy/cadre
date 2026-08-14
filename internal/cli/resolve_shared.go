package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/deagy/cadre/cli/internal/config"
	"github.com/deagy/cadre/cli/internal/platform"
	"gopkg.in/yaml.v3"
)

// ResolveSharedCmd is the `cadre resolve-shared` command: resolves the
// effective content of a roster/shared/<filename> default, optionally
// extended or overridden by a project-local .agents/shared/<filename>
// overlay.
func ResolveSharedCmd(args []string) int {
	fs := flag.NewFlagSet("cadre resolve-shared", flag.ContinueOnError)
	setUsage(fs, "resolve-shared", usageResolveShared)
	project := fs.String("project", "", "Directory to resolve overlays from (default: cwd)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: cadre resolve-shared "+usageResolveShared)
		return 2
	}
	filename := fs.Arg(0)

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
	sharedDir := filepath.Join(repoRoot, "roster", "shared")

	result, err := config.ResolveSharedConfig(sharedDir, filename, *project)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	if result.IsText {
		text := result.Text
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		fmt.Print(text)
		return 0
	}

	if strings.HasSuffix(strings.ToLower(filename), ".json") {
		data, err := json.MarshalIndent(result.Structured, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "cadre resolve-shared: %v\n", err)
			return 1
		}
		fmt.Println(string(data))
	} else {
		data, err := yaml.Marshal(result.Structured)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cadre resolve-shared: %v\n", err)
			return 1
		}
		fmt.Print(string(data))
	}
	return 0
}
