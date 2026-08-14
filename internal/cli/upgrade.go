package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/deagy/cadre/cli/internal/orchestration"
)

// UpgradeCmd is the `cadre upgrade` command: check for newer versions and apply updates.
// cliReleaseTagPrefix is the tag namespace .github/workflows/release.yml's
// cli-publish job creates for the compiled CLI.
const cliReleaseTagPrefix = "cli-v"

func UpgradeCmd(args []string) int {
	fs := flag.NewFlagSet("cadre upgrade", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	checkOnly := fs.Bool("check", false, "only check for updates, don't install")
	force := fs.Bool("force", false, "update without confirmation")
	help := fs.Bool("help", false, "show this help message")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *help {
		fmt.Fprintf(os.Stderr, "usage: cadre upgrade [--check|--force|--help]\n")
		fmt.Fprintf(os.Stderr, "\nCheck for and install Cadre CLI updates.\n")
		fmt.Fprintf(os.Stderr, "\n--check   Only check for updates, don't install\n")
		fmt.Fprintf(os.Stderr, "--force   Update without confirmation\n")
		fmt.Fprintf(os.Stderr, "--help    Show this help message\n")
		return 0
	}

	currentVersion, err := getInstalledVersion()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre upgrade: %v\n", err)
		return 1
	}
	fmt.Printf("Current version: %s\n", currentVersion)
	fmt.Println("Checking for updates...")

	result, err := fetchLatestRelease()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cadre upgrade: could not reach GitHub to check for a %s* release\n", cliReleaseTagPrefix)
		fmt.Fprintf(os.Stderr, "  check your internet connection or try again later\n")
		return 1
	}
	if result == nil {
		fmt.Fprintf(os.Stderr, "cadre upgrade: could not parse release information\n")
		return 1
	}

	latestVersion := result["version"].(string)
	releaseURL := result["release_url"].(string)

	comparison := compareVersions(currentVersion, latestVersion)

	if comparison == 0 {
		fmt.Printf("✓ Cadre %s is up to date\n", currentVersion)
		return 0
	}

	if comparison > 0 {
		fmt.Printf("✓ Cadre %s is newer than the latest release (%s)\n", currentVersion, latestVersion)
		return 0
	}

	// Update available
	if *checkOnly {
		fmt.Printf("Update available: %s → %s\n", currentVersion, latestVersion)
		fmt.Printf("Release notes: %s\n", releaseURL)
		return 0
	}

	kind, root := detectInstallKind()
	tag, _ := result["tag"].(string)

	// Only the wheel channel is updated in place; every other kind prints an
	// instruction and changes nothing, so there is nothing to confirm.
	if kind != orchestration.InstallKindUnknown {
		return updateCadre(kind, root, tag)
	}

	if *force {
		return updateCadre(kind, root, tag)
	}

	if !promptUpdate(currentVersion, latestVersion, releaseURL) {
		fmt.Println("Update cancelled")
		return 0
	}

	return updateCadre(kind, root, tag)
}

func getInstalledVersion() (string, error) {
	// Use the existing CLIVersion function which locates and parses the
	// VERSION marker from cadre_cli/_version.py (or the vendored wheel layout).
	// First, find the repo root.
	repoRoot, err := findRepoRoot()
	if err != nil {
		return "", err
	}
	return CLIVersion(repoRoot)
}

func findRepoRoot() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	current := filepath.Dir(exe)

	// Search up to 10 levels
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(current, "bin", "cadre")); err == nil {
			return current, nil
		}
		if _, err := os.Stat(filepath.Join(current, "pyproject.toml")); err == nil {
			return current, nil
		}
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	// Fallback: assume repo root 3 levels up from this executable
	return filepath.Join(filepath.Dir(exe), "..", "..", ".."), nil
}

// fetchLatestRelease returns the newest `cli-v*` GitHub release.
//
// This used to query PyPI. That was coherent for exactly one of the four
// install kinds: the pip/pipx wheel. A checkout, a `go install` binary and a
// plugin-cache install are none of them updated from PyPI, and a wheel
// publish can lag or fail independently of the binaries. The CLI's own
// release job (.github/workflows/release.yml's cli-publish) tags
// `cli-v<version>` from cadre_cli/_version.py and attaches the per-platform
// binaries there, so that tag is the one source every install kind shares.
//
// /releases/latest is not usable here: this repository also publishes
// `plugin-v*` and kernel tags, and "latest" is whichever was cut most
// recently regardless of prefix.
func fetchLatestRelease() (map[string]any, error) {
	url := "https://api.github.com/repos/deagy/cadre/releases?per_page=30"
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var releases []map[string]any
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, err
	}

	for _, release := range releases {
		tag, _ := release["tag_name"].(string)
		if !strings.HasPrefix(tag, cliReleaseTagPrefix) {
			continue
		}
		if draft, _ := release["draft"].(bool); draft {
			continue
		}
		version := strings.TrimPrefix(tag, cliReleaseTagPrefix)
		if version == "" {
			continue
		}
		releaseURL, _ := release["html_url"].(string)
		if releaseURL == "" {
			releaseURL = "https://github.com/deagy/cadre/releases/tag/" + tag
		}
		return map[string]any{"version": version, "release_url": releaseURL, "tag": tag}, nil
	}
	return nil, fmt.Errorf("no %s* release found", cliReleaseTagPrefix)
}

// detectInstallKind reuses the classification cadre doctor already
// performs, rather than re-deriving it here.
//
// The previous implementation looked for a pyproject.toml, then shelled out
// to `pipx list --json`, then defaulted to "pip" -- so a checkout whose
// probe missed was told to run `pip install --upgrade cadre`, installing a
// wheel over the CLI it was already running from a git tree.
// orchestration.ClassifyRunningBinary distinguishes checkout, go-install,
// plugin-cache and unknown, and is covered by its own tests.
func detectInstallKind() (kind string, root string) {
	report := orchestration.GatherDoctorReport("", "")
	return report.InstallKind, report.InstallRoot
}

func compareVersions(current, latest string) int {
	currentParts := parseVersion(current)
	latestParts := parseVersion(latest)

	// Compare tuples
	minLen := len(currentParts)
	if len(latestParts) < minLen {
		minLen = len(latestParts)
	}

	for i := 0; i < minLen; i++ {
		if currentParts[i] < latestParts[i] {
			return -1
		}
		if currentParts[i] > latestParts[i] {
			return 1
		}
	}

	// If all equal so far, check which has more components
	if len(currentParts) < len(latestParts) {
		return -1
	}
	if len(currentParts) > len(latestParts) {
		return 1
	}

	return 0
}

func parseVersion(v string) []int {
	// Extract base version (e.g., "1.2.3" from "1.2.3rc1")
	re := regexp.MustCompile(`^(\d+(?:\.\d+)*)`)
	match := re.FindString(v)
	if match == "" {
		return []int{0}
	}

	parts := strings.Split(match, ".")
	var result []int
	for _, part := range parts {
		if n, err := strconv.Atoi(part); err == nil {
			result = append(result, n)
		}
	}

	// If there's a pre-release suffix, mark it as older by appending -1
	if len(v) > len(match) {
		result = append(result, -1)
	} else {
		result = append(result, 0)
	}

	if len(result) == 0 {
		return []int{0}
	}
	return result
}

func promptUpdate(current, latest, releaseURL string) bool {
	fmt.Printf("\nCadre update available!\n")
	fmt.Printf("  Current version: %s\n", current)
	fmt.Printf("  Latest version:  %s\n", latest)
	fmt.Printf("  Release notes:   %s\n", releaseURL)
	fmt.Println()

	fmt.Print("Update Cadre now? (y/n) [n]: ")
	var response string
	_, _ = fmt.Scanln(&response)
	return strings.ToLower(strings.TrimSpace(response)) == "y"
}

// updateCadre routes on how this binary was installed. Only the wheel
// channel is updated in place; the others print the one correct instruction
// for their kind, because `git pull` in someone's working tree and a
// marketplace refresh are not this command's to perform unprompted.
func updateCadre(kind, root, tag string) int {
	switch kind {
	case orchestration.InstallKindCheckout:
		fmt.Println("cadre: running from a git checkout" + locatedAt(root) + ".")
		fmt.Println("  To update:")
		fmt.Println("    git -C " + displayRoot(root) + " pull --ff-only")
		fmt.Println()
		fmt.Println("  bin/cadre rebuilds the Go binary on the next invocation, so no")
		fmt.Println("  separate build step is needed. If the pull touched roster/,")
		fmt.Println("  .agents/skills/ or AGENTS.md, re-run the regeneration sequence in")
		fmt.Println("  roster/RUNBOOK.md section 17 before committing anything.")
		return 0

	case orchestration.InstallKindGoInstall:
		target := "github.com/deagy/cadre/cmd/cadre@" + tag
		fmt.Println("cadre: installed with `go install`" + locatedAt(root) + ".")
		fmt.Println("  To update:")
		fmt.Println("    go install " + target)
		return 0

	case orchestration.InstallKindPluginCache:
		fmt.Println("cadre: running from a Claude Code plugin cache" + locatedAt(root) + ".")
		fmt.Println("  This copy is managed by the plugin marketplace, not by this command.")
		fmt.Println("  To update, from Claude Code:")
		fmt.Println("    /plugin marketplace update cadre-team")
		fmt.Println("  then reinstall or update the cadre plugin from that marketplace.")
		return 0
	}

	// Unknown: the pip/pipx wheel is the only remaining channel this command
	// can act on, and it is the one PyPI genuinely serves.
	if pipxHasCadre() {
		fmt.Println("Updating via pipx...")
		command := exec.Command("pipx", "upgrade", "cadre")
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Run(); err != nil {
			return 1
		}
		fmt.Println("\u2713 Cadre updated successfully via pipx")
		fmt.Println("  Run 'cadre --version' to verify the new version")
		return 0
	}

	fmt.Println("Updating via pip...")
	command := exec.Command("pip", "install", "--upgrade", "cadre")
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return 1
	}
	fmt.Println("\u2713 Cadre updated successfully via pip")
	fmt.Println("  Run 'cadre --version' to verify the new version")
	return 0
}

// pipxHasCadre reports whether pipx manages a cadre venv.
func pipxHasCadre() bool {
	output, err := exec.Command("pipx", "list", "--json").Output()
	if err != nil {
		return false
	}
	var data map[string]any
	if err := json.Unmarshal(output, &data); err != nil {
		return false
	}
	venvs, ok := data["venvs"].(map[string]any)
	if !ok {
		return false
	}
	_, present := venvs["cadre"]
	return present
}

func locatedAt(root string) string {
	if root == "" {
		return ""
	}
	return " at " + root
}

func displayRoot(root string) string {
	if root == "" {
		return "<checkout>"
	}
	return root
}
