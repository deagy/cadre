package cli

import (
	"fmt"
	"os"

	"github.com/deagy/cadre/cli/internal/config"
)

var secretEnvVars = []string{"GITLAB_SVC_TOKEN", "KNOWLEDGE_EMBEDDING_API_KEY"}

// ConfigCmd is the `cadre config` command: show/path/resolve/set over the
// unified operator-settings resolver (internal/config).
func ConfigCmd(args []string) int {
	if len(args) == 0 || (args[0] != "show" && args[0] != "path" && args[0] != "resolve" && args[0] != "set") {
		fmt.Fprintln(os.Stderr, "usage: cadre config <show|path|resolve KEY|set KEY VALUE>")
		return 2
	}
	switch args[0] {
	case "show":
		return configCmdShow()
	case "path":
		return configCmdPath()
	case "resolve":
		return configCmdResolve(args[1:])
	case "set":
		return configCmdSet(args[1:])
	}
	return 2
}

func configCmdShow() int {
	for _, r := range config.EffectiveSettings("") {
		source := r.OriginPath
		if source == "" {
			source = "(" + string(r.Origin) + ")"
		}
		fmt.Printf("%-40s %-30s origin=%-12s source=%s\n", r.Key, displayValue(r.Value), r.Origin, source)
	}
	fmt.Println()
	fmt.Println("env-only secrets (never read from or written to a config file):")
	for _, name := range secretEnvVars {
		state := "not set"
		if v := os.Getenv(name); v != "" {
			state = "set"
		}
		fmt.Printf("  env-only: %s (%s)\n", name, state)
	}
	return 0
}

func displayValue(v any) string {
	if v == nil {
		return "<nil>"
	}
	if b, ok := v.(*bool); ok {
		if b == nil {
			return "<nil>"
		}
		return fmt.Sprintf("%v", *b)
	}
	return fmt.Sprintf("%v", v)
}

func configCmdPath() int {
	project, err := config.ProjectConfigPath("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre config: %v\n", err)
		return 1
	}
	if project == "" {
		fmt.Println("project-local: not found")
	} else {
		fmt.Printf("project-local: %s\n", project)
	}
	global, err := config.GlobalConfigPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre config: %v\n", err)
		return 1
	}
	fmt.Printf("user-global:   %s\n", global)
	return 0
}

func configCmdResolve(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: cadre config resolve <key>")
		return 2
	}
	key := args[0]

	interactive := os.Getenv("CADRE_INTERACTIVE") == "1"
	var value any
	var err error
	if interactive {
		input, output, ok := config.OpenTTYIO()
		if ok {
			werr := config.WithStdoutTTYOverride(true, func() error {
				var innerErr error
				value, innerErr = config.ResolveOptionalWithIO(key, "", input, output)
				return innerErr
			})
			err = werr
		} else {
			value, err = config.ResolveOptional(key, "")
		}
	} else {
		value, err = config.ResolveOptional(key, "")
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if value != nil {
		fmt.Println(displayValue(value))
	}
	return 0
}

func configCmdSet(argv []string) int {
	tier := "project"
	var positional []string
	for _, arg := range argv {
		switch arg {
		case "--global":
			tier = "global"
		case "--project":
			tier = "project"
		default:
			if len(arg) > 0 && arg[0] == '-' {
				fmt.Fprintf(os.Stderr, "cadre config set: unknown option %s\n", arg)
				return 2
			}
			positional = append(positional, arg)
		}
	}
	if len(positional) != 2 {
		fmt.Fprintln(os.Stderr, "usage: cadre config set [--project|--global] <key> <value>")
		return 2
	}
	key, raw := positional[0], positional[1]
	written, err := config.WriteSetting(key, raw, tier, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre config set: %v\n", err)
		return 1
	}
	fmt.Printf("%s = %s\n", key, raw)
	fmt.Printf("written to %s\n", written)
	return 0
}
