package generators

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PluginPackage holds all generated plugin outputs.
type PluginPackage struct {
	OutputRoot string            // Target directory for plugin
	Files      map[string]string // path -> content
	FilesPaths []string          // Sorted paths written (for reporting)
	Readme     bool              // Whether README.md was written
}

// GeneratePlugin orchestrates the complete plugin package generation.
// It loads the catalog, discovers skills, copies source files, and generates
// all required distribution artifacts (skill package, suite copy, agent wrappers, etc.).
func GeneratePlugin(manifestRoot string, outputRoot string) (*PluginPackage, error) {
	// Resolve output root
	absOutput, err := filepath.Abs(filepath.FromSlash(outputRoot))
	if err != nil {
		return nil, fmt.Errorf("cannot resolve output root: %w", err)
	}

	// Check if target is empty or existing generated plugin
	if _, err := os.Stat(absOutput); err == nil {
		// Directory exists - check if it's empty or a previously generated plugin
		entries, err := os.ReadDir(absOutput)
		if err != nil {
			return nil, fmt.Errorf("cannot read output directory: %w", err)
		}
		if len(entries) > 0 {
			// Check for marker file (.codex-plugin/plugin.json)
			markerPath := filepath.Join(absOutput, ".codex-plugin", "plugin.json")
			if _, err := os.Stat(markerPath); err != nil {
				return nil, fmt.Errorf("--output must be a new directory or an existing generated plugin")
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("cannot access output directory: %w", err)
	}

	// Load catalog for plugin generation
	rosterRoot := filepath.Join(manifestRoot, "roster")
	orderIDs, err := LoadCatalogOrder(filepath.Join(rosterRoot, "catalog-order.txt"))
	if err != nil {
		return nil, fmt.Errorf("cannot load catalog order: %w", err)
	}

	tiers, err := LoadModelTiers(filepath.Join(rosterRoot, "runner-capabilities.json"))
	if err != nil {
		return nil, fmt.Errorf("cannot load model tiers: %w", err)
	}

	discovered, err := DiscoverRoles(rosterRoot)
	if err != nil {
		return nil, fmt.Errorf("cannot discover roles: %w", err)
	}

	allRoles, err := LoadAllRoles(rosterRoot, orderIDs, discovered, tiers)
	if err != nil {
		return nil, fmt.Errorf("cannot load roles: %w", err)
	}

	pkg := &PluginPackage{
		OutputRoot: absOutput,
		Files:      make(map[string]string),
	}

	// Generate essential plugin files
	// 1. Plugin manifests (.codex-plugin/plugin.json, .claude-plugin/plugin.json)
	if err := generatePluginManifests(pkg, manifestRoot); err != nil {
		return nil, fmt.Errorf("cannot generate plugin manifests: %w", err)
	}

	// 2. Skill copies (.agents/skills/* -> plugin/skills/*)
	if err := generateSkillCopies(pkg, manifestRoot); err != nil {
		return nil, fmt.Errorf("cannot generate skill copies: %w", err)
	}

	// 3. Agent wrappers (plugin/agents/*.md for Claude Code)
	if err := generateAgentWrappers(pkg, allRoles); err != nil {
		return nil, fmt.Errorf("cannot generate agent wrappers: %w", err)
	}

	// 4. Suite copy (roster/ -> plugin/suite/roster/)
	if err := generateSuiteCopy(pkg, rosterRoot, allRoles); err != nil {
		return nil, fmt.Errorf("cannot generate suite copy: %w", err)
	}

	// 5. Provider copy (provider/ -> plugin/provider/)
	if err := generateProviderCopy(pkg, manifestRoot, allRoles); err != nil {
		return nil, fmt.Errorf("cannot generate provider copy: %w", err)
	}

	// 6. README.md
	if err := generateReadme(pkg); err != nil {
		return nil, fmt.Errorf("cannot generate README: %w", err)
	}

	// Collect sorted file paths
	for path := range pkg.Files {
		pkg.FilesPaths = append(pkg.FilesPaths, path)
	}
	sort.Strings(pkg.FilesPaths)

	return pkg, nil
}

// generatePluginManifests creates .codex-plugin/plugin.json and .claude-plugin/plugin.json.
func generatePluginManifests(pkg *PluginPackage, manifestRoot string) error {
	// For now, create minimal valid manifests.
	// In production, these would be loaded from hand-authored source files.

	codexManifest := `{
  "name": "cadre",
  "description": "Cadre - specialist subagent roles for SDLC orchestration",
  "version": "0.24.0",
  "minVersion": "0.1.0"
}
`
	pkg.Files[filepath.Join(pkg.OutputRoot, ".codex-plugin", "plugin.json")] = codexManifest

	claudeManifest := `{
  "name": "cadre",
  "description": "Cadre - specialist subagent roles for SDLC orchestration",
  "version": "0.24.0"
}
`
	pkg.Files[filepath.Join(pkg.OutputRoot, ".claude-plugin", "plugin.json")] = claudeManifest

	return nil
}

// generateSkillCopies copies .agents/skills/* to plugin/skills/*.
func generateSkillCopies(pkg *PluginPackage, manifestRoot string) error {
	skillsRoot := filepath.Join(manifestRoot, ".agents", "skills")

	// Walk .agents/skills/ directory
	entries, err := os.ReadDir(skillsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Skills directory optional
		}
		return fmt.Errorf("cannot read skills directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillName := entry.Name()
		skillPath := filepath.Join(skillsRoot, skillName)

		// Copy SKILL.md if it exists
		skillMD := filepath.Join(skillPath, "SKILL.md")
		if content, err := os.ReadFile(skillMD); err == nil {
			outputPath := filepath.Join(pkg.OutputRoot, "skills", skillName, "SKILL.md")
			pkg.Files[outputPath] = string(content)
		}

		// Copy README.md if it exists
		readmePath := filepath.Join(skillPath, "README.md")
		if content, err := os.ReadFile(readmePath); err == nil {
			outputPath := filepath.Join(pkg.OutputRoot, "skills", skillName, "README.md")
			pkg.Files[outputPath] = string(content)
		}

		// Copy references/ directory if it exists
		refsPath := filepath.Join(skillPath, "references")
		if refEntries, err := os.ReadDir(refsPath); err == nil {
			for _, refEntry := range refEntries {
				if refEntry.IsDir() {
					continue
				}
				refFile := filepath.Join(refsPath, refEntry.Name())
				if content, err := os.ReadFile(refFile); err == nil {
					outputPath := filepath.Join(pkg.OutputRoot, "skills", skillName, "references", refEntry.Name())
					pkg.Files[outputPath] = string(content)
				}
			}
		}
	}

	return nil
}

// generateAgentWrappers creates plugin/agents/*.md for all roles.
func generateAgentWrappers(pkg *PluginPackage, roles []RoleMetadata) error {
	for _, role := range roles {
		wrapper := fmt.Sprintf(`# %s

**Capability:** %s
**Phase:** %s
**Model:** %s

Role stub for orchestration. Full role definition: suite/roster/%s/%s/AGENT.md
`,
			role.ID,
			role.Capability,
			role.Phase,
			role.Model,
			role.Phase,
			role.ID,
		)

		outputPath := filepath.Join(pkg.OutputRoot, "agents", role.ID+".md")
		pkg.Files[outputPath] = wrapper
	}

	return nil
}

// generateSuiteCopy copies roster/ to plugin/suite/roster/.
func generateSuiteCopy(pkg *PluginPackage, rosterRoot string, roles []RoleMetadata) error {
	// Copy key files from roster/ that the suite needs
	filesToCopy := []string{
		"catalog.yaml",
		"catalog-order.txt",
		"runner-capabilities.json",
		"_catalog_header.yaml.tmpl",
	}

	for _, filename := range filesToCopy {
		sourcePath := filepath.Join(rosterRoot, filename)
		if content, err := os.ReadFile(sourcePath); err == nil {
			outputPath := filepath.Join(pkg.OutputRoot, "suite", "roster", filename)
			pkg.Files[outputPath] = string(content)
		}
	}

	// Copy each role's AGENT.md and other files
	for _, role := range roles {
		rolePath := filepath.Join(rosterRoot, role.Phase, role.ID)

		// Copy AGENT.md
		agentMD := filepath.Join(rolePath, "AGENT.md")
		if content, err := os.ReadFile(agentMD); err == nil {
			outputPath := filepath.Join(pkg.OutputRoot, "suite", "roster", role.Phase, role.ID, "AGENT.md")
			pkg.Files[outputPath] = string(content)
		}
	}

	return nil
}

// generateProviderCopy copies provider/ to plugin/provider/ with path rewriting.
func generateProviderCopy(pkg *PluginPackage, manifestRoot string, roles []RoleMetadata) error {
	providerRoot := filepath.Join(manifestRoot, "provider")

	// Copy provider.json
	if content, err := os.ReadFile(filepath.Join(providerRoot, "provider.json")); err == nil {
		outputPath := filepath.Join(pkg.OutputRoot, "provider", "provider.json")
		pkg.Files[outputPath] = string(content)
	}

	// Copy agent-catalog.json with path rewriting
	if content, err := os.ReadFile(filepath.Join(providerRoot, "agent-catalog.json")); err == nil {
		// Rewrite definition paths from "roles/..." to "suite/roster/..."
		updated := strings.ReplaceAll(string(content), "roles/", "suite/roster/")
		outputPath := filepath.Join(pkg.OutputRoot, "provider", "agent-catalog.json")
		pkg.Files[outputPath] = updated
	}

	// Copy profiles/ and extensions/ if they exist
	for _, dir := range []string{"profiles", "extensions"} {
		sourcePath := filepath.Join(providerRoot, dir)
		if entries, err := os.ReadDir(sourcePath); err == nil {
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
					filePath := filepath.Join(sourcePath, entry.Name())
					if content, err := os.ReadFile(filePath); err == nil {
						outputPath := filepath.Join(pkg.OutputRoot, "provider", dir, entry.Name())
						pkg.Files[outputPath] = string(content)
					}
				}
			}
		}
	}

	return nil
}

// generateReadme creates plugin/README.md with templated content.
func generateReadme(pkg *PluginPackage) error {
	readme := "# Cadre Plugin\n\n" +
		"A self-contained Cadre plugin package with all specialist roles, skills, and supporting files.\n\n" +
		"## Installation\n\n" +
		"**Claude Code:**\n" +
		"```\n" +
		"/plugin marketplace add deagy/cadre\n" +
		"```\n\n" +
		"**Codex CLI:**\n" +
		"```\n" +
		"codex plugin install cadre\n" +
		"```\n\n" +
		"## Contents\n\n" +
		"- **agents/** - Claude Code subagent role wrappers\n" +
		"- **skills/** - Packaged Cadre skills (orchestration, governance, etc.)\n" +
		"- **suite/roster/** - Complete role definitions (phases, capabilities, AGENT.md)\n" +
		"- **provider/** - Provider bundle (profiles, extensions, agent catalog)\n\n" +
		"## Role Phases\n\n" +
		"This plugin includes roles organized by SDLC phase:\n" +
		"- **planning** - Requirements, product intent, delivery sequencing\n" +
		"- **design** - Architecture, API contracts, threat modeling\n" +
		"- **build** - Implementation across all technology stacks\n" +
		"- **verify** - Testing, validation, quality assurance\n" +
		"- **review** - Code review, compliance, security\n" +
		"- **release** - Deployment, conformance claims, evidence\n" +
		"- **operations** - Reliability, cost, vendor management\n" +
		"- **support** - Escalation, incident response\n" +
		"- **security** - Cryptography, policy, supply chain\n" +
		"- **governance** - Policy, audit, compliance frameworks\n" +
		"- **evidence** - Results curation and management\n" +
		"- **authority** - Human lifecycle decision authority\n" +
		"- **documentation** - Technical content and architectural docs\n" +
		"- **data** - Data governance and transformation\n" +
		"- **knowledge** - Knowledge store stewardship\n\n" +
		"## For More Information\n\n" +
		"Visit the [Cadre repository](https://github.com/deagy/cadre) for:\n" +
		"- Complete role catalog and definitions\n" +
		"- Workflow examples and best practices\n" +
		"- Contributing guidelines\n" +
		"- Governance framework documentation\n"

	outputPath := filepath.Join(pkg.OutputRoot, "README.md")
	pkg.Files[outputPath] = readme
	pkg.Readme = true

	return nil
}

// WritePluginFiles writes all generated files to disk.
func WritePluginFiles(pkg *PluginPackage) error {
	for path, content := range pkg.Files {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("cannot create directory %s: %w", dir, err)
		}

		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fmt.Errorf("cannot write %s: %w", path, err)
		}
	}

	return nil
}

// CheckPluginPackage validates that generated plugin is current without writing.
func CheckPluginPackage(pkg *PluginPackage) (bool, []string, error) {
	var staleFiles []string

	for path, expectedContent := range pkg.Files {
		actual, err := os.ReadFile(path)
		if err != nil || string(actual) != expectedContent {
			relPath, _ := filepath.Rel(filepath.Dir(pkg.OutputRoot), path)
			staleFiles = append(staleFiles, relPath)
		}
	}

	sort.Strings(staleFiles)
	return len(staleFiles) == 0, staleFiles, nil
}
