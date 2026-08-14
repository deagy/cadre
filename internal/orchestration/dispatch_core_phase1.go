package orchestration

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Phase 1: Role Resolution, Sandbox Management, Child Process Spawning

// LoadKnownRoleIDs loads the set of valid role IDs from the catalog
func LoadKnownRoleIDs(catalogPath string) (map[string]bool, error) {
	// This requires reading and parsing the catalog YAML
	// For now, stub - full implementation requires YAML parsing
	// In production, this reads roster/catalog.yaml and extracts role IDs
	return make(map[string]bool), nil
}

// ResolveRoleFileCodex resolves a Codex role by searching through tiers
func ResolveRoleFileCodex(
	roleID string,
	projectRoot string,
	globalRoot string,
	pluginRoot string,
	mode string,
) (*ResolvedRole, error) {
	if err := ValidateRoleID(roleID); err != nil {
		return nil, err
	}

	// Tier search order: project -> global -> plugin
	tiers := []struct {
		name string
		root string
	}{
		{"project", projectRoot},
		{"global", globalRoot},
		{"plugin", pluginRoot},
	}

	for _, tier := range tiers {
		filename := roleID + ".toml"
		candidate := filepath.Join(tier.root, filename)

		// Check if file exists (using lexists semantics - follow symlinks but check existence)
		if _, err := os.Lstat(candidate); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, &DispatchDenied{Reason: fmt.Sprintf("cannot access %s-tier role file %s: %v", tier.name, candidate, err)}
		}

		// Ensure file is contained within tier root (prevent directory traversal)
		if err := ensureContained(candidate, tier.root); err != nil {
			return nil, &DispatchDenied{Reason: err.Error()}
		}

		// Project tier with write mode requires git-clean check
		if tier.name == "project" && mode == ModeRepositoryEdit {
			gitClean, err := isProjectTierGitClean(candidate, projectRoot)
			if err != nil {
				return nil, err
			}
			if !gitClean {
				return nil, &ProjectTierNotGitCleanError{
					Reason: fmt.Sprintf("project-tier role file is not git-clean; commit it or use mode=planning-review-only: %s", candidate),
				}
			}
		}

		// Read and validate role file
		content, err := readRoleFileCapped(candidate, MaxRoleFileBytes)
		if err != nil {
			if os.IsNotExist(err) {
				continue // File disappeared between checks - try next tier
			}
			return nil, &DispatchDenied{Reason: fmt.Sprintf("refusing to read %s-tier role file %s: %v", tier.name, candidate, err)}
		}

		// Parse TOML fields from file
		fields, err := extractTOMLFields(string(content), candidate)
		if err != nil {
			return nil, &DispatchDenied{Reason: err.Error()}
		}

		developerInstructions := fields["developer_instructions"]
		if developerInstructions == "" {
			return nil, &DispatchDenied{Reason: fmt.Sprintf("%s-tier role file is missing developer_instructions: %s", tier.name, candidate)}
		}

		model := fields["model"]
		if model == "" {
			return nil, &DispatchDenied{Reason: fmt.Sprintf("%s-tier role file is missing required model: %s", tier.name, candidate)}
		}

		// Create resolved role
		_ = sha256.Sum256([]byte(developerInstructions)) // digest computed but not used in stub
		role := &ResolvedRole{
			ID:                developerInstructions,
			FilePath:          candidate,
			DeveloperInstructs: developerInstructions,
			Model:             model,
			SandboxMode:       fields["sandbox_mode"],
		}
		return role, nil
	}

	return nil, &DispatchUnavailable{Reason: fmt.Sprintf("no .toml file found for role_id %q at any resolution tier", roleID)}
}

// ResolveClaude CodeRoleFile resolves a Claude Code role from .md file
func ResolveClaudeCodeRoleFile(
	roleID string,
	projectRoot string,
	pluginSearchRoot string,
	mode string,
) (*ResolvedRole, error) {
	if err := ValidateRoleID(roleID); err != nil {
		return nil, err
	}

	projectTierRoot := filepath.Join(projectRoot, ".claude", "agents")
	projectCandidate := filepath.Join(projectTierRoot, roleID+".md")

	var tier, candidate string

	// Check project tier first
	if _, err := os.Lstat(projectTierRoot); err == nil {
		if _, err := os.Lstat(projectCandidate); err == nil {
			if err := ensureContained(projectCandidate, projectTierRoot); err != nil {
				return nil, &DispatchDenied{Reason: err.Error()}
			}
			tier = "project"
			candidate = projectCandidate
		}
	}

	// Fall back to plugin tier if not found
	if candidate == "" {
		pluginCandidate, err := findClaudePluginRoleFile(roleID, pluginSearchRoot)
		if err != nil {
			return nil, err
		}
		if pluginCandidate == "" {
			return nil, &DispatchUnavailable{Reason: fmt.Sprintf("no .md file found for role_id %q at any Claude Code resolution tier", roleID)}
		}
		tier = "plugin"
		candidate = pluginCandidate
	}

	// Project tier with write mode requires git-clean check
	if tier == "project" && mode == ModeRepositoryEdit {
		gitClean, err := isProjectTierGitClean(candidate, projectRoot)
		if err != nil {
			return nil, err
		}
		if !gitClean {
			return nil, &ProjectTierNotGitCleanError{
				Reason: fmt.Sprintf("project-tier role file is not git-clean; commit it or use mode=planning-review-only: %s", candidate),
			}
		}
	}

	// Read and validate role file
	content, err := readRoleFileCapped(candidate, MaxRoleFileBytes)
	if err != nil {
		return nil, &DispatchDenied{Reason: fmt.Sprintf("refusing to read %s-tier role file %s: %v", tier, candidate, err)}
	}

	// Parse markdown frontmatter
	fields, body, err := extractMarkdownFrontmatter(string(content), candidate)
	if err != nil {
		return nil, &DispatchDenied{Reason: err.Error()}
	}

	developerInstructions := strings.TrimSpace(body)
	if developerInstructions == "" {
		return nil, &DispatchDenied{Reason: fmt.Sprintf("%s-tier role file has no body to use as developer_instructions: %s", tier, candidate)}
	}

	model := fields["model"]
	if model == "" {
		return nil, &DispatchDenied{Reason: fmt.Sprintf("%s-tier role file is missing required model: %s", tier, candidate)}
	}

	// Claude Code wrappers never declare sandbox_mode - always read-only
	role := &ResolvedRole{
		ID:                 developerInstructions,
		FilePath:           candidate,
		DeveloperInstructs: developerInstructions,
		Model:              model,
		SandboxMode:        "", // Always read-only for Claude Code
	}
	return role, nil
}

// Helper functions

func ensureContained(path, root string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("cannot resolve path %s: %v", path, err)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("cannot resolve root %s: %v", root, err)
	}

	// Ensure path is under root
	if !strings.HasPrefix(absPath, absRoot) {
		return fmt.Errorf("path %s is not contained in %s", absPath, absRoot)
	}
	return nil
}

func isProjectTierGitClean(filePath, projectRoot string) (bool, error) {
	// Check if file is tracked and clean in git
	// Run: git -C <projectRoot> ls-files --error-unmatch <filePath>
	cmd := exec.Command("git", "-C", projectRoot, "ls-files", "--error-unmatch", filePath)
	if err := cmd.Run(); err != nil {
		// File not tracked in git
		return false, nil
	}

	// Check if file has modifications
	cmd = exec.Command("git", "-C", projectRoot, "diff", "--quiet", filePath)
	if err := cmd.Run(); err != nil {
		// File has modifications
		return false, nil
	}

	// Check staged changes
	cmd = exec.Command("git", "-C", projectRoot, "diff", "--quiet", "--cached", filePath)
	if err := cmd.Run(); err != nil {
		// File has staged changes
		return false, nil
	}

	return true, nil
}

func readRoleFileCapped(path string, maxBytes int) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if fi.Size() > int64(maxBytes) {
		return nil, fmt.Errorf("role file exceeds maximum size %d bytes: %s (%d bytes)", maxBytes, path, fi.Size())
	}
	return os.ReadFile(path)
}

func extractTOMLFields(content, source string) (map[string]string, error) {
	// Simple TOML field extraction for role files
	// Looks for: key = "value" or key = 'value'
	fields := make(map[string]string)
	rx := regexp.MustCompile(`^\s*(\w+)\s*=\s*['"](.*?)['"]`)

	for _, line := range strings.Split(content, "\n") {
		matches := rx.FindStringSubmatch(line)
		if len(matches) == 3 {
			key := matches[1]
			value := matches[2]
			fields[key] = value
		}
	}

	return fields, nil
}

func extractMarkdownFrontmatter(content, source string) (map[string]string, string, error) {
	// Parse markdown frontmatter: ---\nkey: value\n...\n---\nbody
	if !strings.HasPrefix(content, "---\n") {
		return nil, "", fmt.Errorf("%s: expected a `---`-delimited frontmatter block at the start", source)
	}

	closing := strings.Index(content[4:], "\n---")
	if closing == -1 {
		return nil, "", fmt.Errorf("%s: frontmatter is missing its closing `---` delimiter", source)
	}

	frontmatterText := content[4 : 4+closing]
	body := strings.TrimLeft(content[4+closing+4:], "\n")

	fields := make(map[string]string)
	rx := regexp.MustCompile(`^(\w+):\s*(.*)$`)

	for _, line := range strings.Split(frontmatterText, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		matches := rx.FindStringSubmatch(line)
		if len(matches) == 3 {
			key := matches[1]
			value := strings.TrimSpace(matches[2])
			fields[key] = value
		}
	}

	return fields, body, nil
}

func findClaudePluginRoleFile(roleID, pluginSearchRoot string) (string, error) {
	// Look for installed Claude Code plugin role files
	// Path: <pluginSearchRoot>/*/*/*/agents/<roleID>.md
	if _, err := os.Lstat(pluginSearchRoot); err != nil {
		return "", nil // Plugin dir doesn't exist
	}

	pattern := filepath.Join(pluginSearchRoot, "*", "*", "*", "agents", roleID+".md")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", err
	}

	if len(matches) == 0 {
		return "", nil
	}

	if len(matches) > 1 {
		return "", &DispatchDenied{
			Reason: fmt.Sprintf("multiple installed plugin copies of %q found under %s; use a project-tier .claude/agents/%s.md override to disambiguate", roleID, pluginSearchRoot, roleID),
		}
	}

	return matches[0], nil
}

// ProjectTierNotGitCleanError represents a git-clean validation failure
type ProjectTierNotGitCleanError struct {
	Reason string
}

func (e ProjectTierNotGitCleanError) Error() string {
	return e.Reason
}

// SpawnAndWait spawns a child process and waits for completion
func SpawnAndWait(
	cmd string,
	args []string,
	env map[string]string,
	timeout float64,
) (map[string]any, error) {
	// Convert env map to []string for exec.Cmd
	envSlice := make([]string, 0, len(env))
	for k, v := range env {
		envSlice = append(envSlice, k+"="+v)
	}

	command := exec.Command(cmd, args...)
	command.Env = envSlice

	// Capture output
	output, err := command.CombinedOutput()
	if err != nil {
		exitCode := 1
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		}
		return map[string]any{
			"status":     "error",
			"exit_code":  exitCode,
			"error":      err.Error(),
			"output":     string(output),
		}, nil
	}

	return map[string]any{
		"status":     "success",
		"exit_code":  0,
		"output":     string(output),
	}, nil
}
