package orchestration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
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

		// The digest was computed and thrown away here, and ID was set to the
		// instructions themselves -- so the audit record carried no way to
		// tell which role text actually ran, and ID held a whole system
		// prompt under a name that reads like an identifier.
		digest := sha256.Sum256([]byte(developerInstructions))
		return &ResolvedRole{
			ID:                 roleID,
			FilePath:           candidate,
			Tier:               tier.name,
			InstructionsSHA256: hex.EncodeToString(digest[:]),
			DeveloperInstructs: developerInstructions,
			Model:              model,
			SandboxMode:        fields["sandbox_mode"],
		}, nil
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
	digest := sha256.Sum256([]byte(developerInstructions))
	return &ResolvedRole{
		ID:                 roleID,
		FilePath:           candidate,
		Tier:               tier,
		InstructionsSHA256: hex.EncodeToString(digest[:]),
		DeveloperInstructs: developerInstructions,
		Model:              model,
		SandboxMode:        "", // Always read-only for Claude Code
	}, nil
}

// Helper functions

func ensureContained(path, root string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("cannot resolve path %s: %w", path, err)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("cannot resolve root %s: %w", root, err)
	}

	// Compared as path components, not as a string prefix. "/srv/project"
	// is a string prefix of "/srv/project-attacker", so the prefix form
	// called a sibling directory contained.
	relative, err := filepath.Rel(absRoot, absPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path %s is not contained in %s", absPath, absRoot)
	}
	return nil
}

// gitCleanTimeout bounds each git call. Without one, a stalled git -- a
// contended index.lock, a slow or unresponsive filesystem -- hangs the
// dispatch forever rather than failing it. The Python original bounded this
// at the same 10 seconds.
const gitCleanTimeout = 10 * time.Second

// isProjectTierGitClean reports whether a project-tier role file is tracked
// and has no staged or unstaged modification.
//
// A project-tier role file is the one an attacker with repository write
// access controls most directly, so in scoped-repository-edit mode it must
// be in git history before its instructions are trusted. Every failure mode
// -- untracked, modified, git missing, git erroring, git timing out --
// answers "not clean". Failing open here would make the check absent exactly
// when something is wrong with the repository.
func isProjectTierGitClean(filePath, projectRoot string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitCleanTimeout)
	defer cancel()

	// Tracked at all, then unmodified in the worktree, then unmodified in
	// the index. A file failing any one of the three is not clean.
	for _, args := range [][]string{
		{"ls-files", "--error-unmatch", filePath},
		{"diff", "--quiet", filePath},
		{"diff", "--quiet", "--cached", filePath},
	} {
		command := exec.CommandContext(ctx, "git", append([]string{"-C", projectRoot}, args...)...)
		if err := command.Run(); err != nil {
			return false, nil
		}
	}
	return true, nil
}

// readRoleFileCapped reads a role file, refusing anything that is not a
// regular file the caller named directly.
//
// A role file is the dispatched agent's authority: its developer_instructions
// become the child's system prompt, and its sandbox_mode helps decide what
// the child may touch. Reading the wrong bytes here is not a file-handling
// bug, it is a privilege decision made from an attacker's text.
//
// This was Lstat-for-size followed by os.ReadFile, which:
//
//   - followed a symlink, so a role file symlinked at any tier was read from
//     wherever it pointed, inside the project or not;
//   - took the size cap from the Lstat, which for a symlink is the length of
//     the link target string -- a few dozen bytes -- so the cap did not
//     apply to what was actually read;
//   - left a window between the stat and the open, which matters because
//     dispatch runs team members concurrently against one project root.
//
// The open now carries O_NOFOLLOW where the platform has it, the
// regular-file check is made against the open descriptor rather than the
// path, and the cap is enforced while reading. Exceeding it is a refusal,
// never a truncation: a role whose instructions were silently cut in half
// still dispatches, with authority nobody wrote.
func readRoleFileCapped(path string, maxBytes int) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|noFollowFlag, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("refusing non-regular role file: %s", path)
	}

	// One byte past the cap, so a file exactly at the limit is accepted and
	// anything larger is detected without reading all of it.
	content, err := io.ReadAll(io.LimitReader(file, int64(maxBytes)+1))
	if err != nil {
		return nil, err
	}
	if len(content) > maxBytes {
		return nil, fmt.Errorf("role file exceeds maximum size %d bytes: %s", maxBytes, path)
	}
	return content, nil
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
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
		}
		return map[string]any{
			"status":    "error",
			"exit_code": exitCode,
			"error":     err.Error(),
			"output":    string(output),
		}, nil
	}

	return map[string]any{
		"status":    "success",
		"exit_code": 0,
		"output":    string(output),
	}, nil
}
