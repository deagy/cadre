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
)

// UpgradeCmd is the `cadre upgrade` command: check for newer versions and apply updates.
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
		fmt.Fprintf(os.Stderr, "cadre upgrade: could not reach PyPI to check for updates\n")
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

	installMethod := detectInstallMethod()

	if *force {
		return updateCadre(installMethod)
	}

	if !promptUpdate(currentVersion, latestVersion, releaseURL) {
		fmt.Println("Update cancelled")
		return 0
	}

	return updateCadre(installMethod)
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

func fetchLatestRelease() (map[string]any, error) {
	url := "https://pypi.org/pypi/cadre/json"
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

	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}

	info, ok := data["info"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("no info field in response")
	}

	version, ok := info["version"].(string)
	if !ok || version == "" {
		return nil, fmt.Errorf("no version in response")
	}

	releaseURL := fmt.Sprintf("https://pypi.org/project/cadre/%s/", version)
	return map[string]any{
		"version":     version,
		"release_url": releaseURL,
	}, nil
}

func detectInstallMethod() string {
	repoRoot, err := findRepoRoot()
	if err == nil {
		if _, err := os.Stat(filepath.Join(repoRoot, "pyproject.toml")); err == nil {
			if _, err := os.Stat(filepath.Join(repoRoot, "bin", "cadre")); err == nil {
				return "source"
			}
		}
		if _, err := os.Stat(filepath.Join(repoRoot, "..", "pyproject.toml")); err == nil {
			if _, err := os.Stat(filepath.Join(repoRoot, "..", "bin", "cadre")); err == nil {
				return "source"
			}
		}
	}

	// Try to detect pipx installation
	cmd := exec.Command("pipx", "list", "--json")
	output, err := cmd.Output()
	if err == nil {
		var data map[string]any
		if err := json.Unmarshal(output, &data); err == nil {
			venvs, ok := data["venvs"].(map[string]any)
			if ok {
				if _, ok := venvs["cadre"]; ok {
					return "pipx"
				}
			}
		}
	}

	// Default to pip
	return "pip"
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

func updateCadre(installMethod string) int {
	if installMethod == "source" {
		fmt.Println("cadre: Running from a source checkout. To update, use:")
		fmt.Println("  git pull origin main")
		fmt.Println("  make generate")
		return 0
	}

	if installMethod == "pipx" {
		fmt.Println("Updating via pipx...")
		cmd := exec.Command("pipx", "upgrade", "cadre")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return 1
		}
		fmt.Println("✓ Cadre updated successfully via pipx")
		fmt.Println("  Run 'cadre --version' to verify the new version")
		return 0
	}

	// pip
	fmt.Println("Updating via pip...")
	cmd := exec.Command("pip", "install", "--upgrade", "cadre")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return 1
	}
	fmt.Println("✓ Cadre updated successfully via pip")
	fmt.Println("  Run 'cadre --version' to verify the new version")
	return 0
}
