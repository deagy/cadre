package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/deagy/cadre/cli/internal/orchestration"
)

// ProfileCmd is the `cadre profile` command: a faithful port of
// profile_diff.py. Read-only: never writes to, re-syncs, or remediates
// anything belonging to a consuming project.
func ProfileCmd(args []string) int {
	if len(args) == 0 || args[0] != "diff" {
		fmt.Fprintln(os.Stderr, "usage: cadre profile diff --copy-provider PATH --copy-profile PATH [options]")
		return 2
	}
	return profileDiff(args[1:])
}

func profileDiff(args []string) int {
	fs := flag.NewFlagSet("cadre profile diff", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	copyProvider := fs.String("copy-provider", "", "path to the project's copied provider.json (required)")
	copyProfile := fs.String("copy-profile", "", "path to the project's copied profile.json (required)")
	originalProvider := fs.String("original-provider", "", "path to the provider.json snapshot COPY was originally captured from, if kept")
	originalProfile := fs.String("original-profile", "", "path to the profile.json snapshot COPY was originally captured from, if kept")
	currentProvider := fs.String("current-provider", "", "override this suite's current provider.json (default: auto-detected)")
	currentProfile := fs.String("current-profile", "", "override this suite's current profile.json (default: auto-detected from --profile-id)")
	profileID := fs.String("profile-id", "secure-cloud", "profile id used to resolve the default --current-profile path")
	asJSON := fs.Bool("json", false, "emit machine-readable JSON instead of text")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *copyProvider == "" || *copyProfile == "" {
		fmt.Fprintln(os.Stderr, "cadre profile diff: --copy-provider and --copy-profile are required")
		return 2
	}

	resolvedCurrentProvider := *currentProvider
	resolvedCurrentProfile := *currentProfile
	if resolvedCurrentProvider == "" || resolvedCurrentProfile == "" {
		defaultProvider, defaultProfile, ok := orchestration.FindDefaultCurrentPaths(*profileID)
		if !ok {
			fmt.Fprintln(os.Stderr,
				"cadre profile diff: could not auto-detect this suite's current provider.json/profile.json; "+
					"pass --current-provider and --current-profile explicitly")
			return 1
		}
		if resolvedCurrentProvider == "" {
			resolvedCurrentProvider = defaultProvider
		}
		if resolvedCurrentProfile == "" {
			resolvedCurrentProfile = defaultProfile
		}
	}

	results, err := orchestration.RunProfileDiff(orchestration.ProfileDiffRequest{
		CopyProviderPath: *copyProvider, CopyProfilePath: *copyProfile,
		CurrentProviderPath: resolvedCurrentProvider, CurrentProfilePath: resolvedCurrentProfile,
		OriginalProviderPath: *originalProvider, OriginalProfilePath: *originalProfile,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre profile diff: %v\n", err)
		return 1
	}

	if *asJSON {
		payload := orchestration.ToJSONable(results)
		data, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "cadre profile diff: %v\n", err)
			return 1
		}
		fmt.Println(string(data))
	} else {
		fmt.Println("cadre profile diff report")
		fmt.Println(orchestration.Disclaimer)
		fmt.Println()
		for _, name := range []string{"provider", "profile"} {
			for _, line := range orchestration.RenderArtifact(name, results[name]) {
				fmt.Println(line)
			}
			fmt.Println()
		}
		overallDrift := !orchestration.AllCurrent(results)
		if overallDrift {
			fmt.Println("Overall: drift detected in at least one artifact above; no action has been taken or implied.")
		} else {
			fmt.Println("Overall: no drift detected; not an approval or compliance signal.")
		}
	}

	if orchestration.AllCurrent(results) {
		return 0
	}
	return 1
}
