package generators

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// PortClineAgents renders the packaged plugin's agents and skills into the
// Cline preset/skill formats.
//
// A port of plugin/tools/port_cline_agents.py. The substitution vocabulary is
// in cline_tables.go, extracted from that file mechanically rather than
// retyped -- 124 exact-match pairs where one slip changes a generated file in
// a way that reads as intentional.
//
// The port fails loudly on a source-repo-relative reference it does not
// recognise. That is the whole safety property: a new role or a renamed policy
// file must either match the table or stop the generator, rather than shipping
// a leaked path into a plugin someone installs. Extend the tables; do not
// loosen the check.

var (
	clineRoutingConfigRe   = regexp.MustCompile(`roster/orchestration/\s+routing\.json's`)
	clineSharedPolicyRe    = regexp.MustCompile(`(?m)^# Shared policy: roster/shared/`)
	clineRoleHeadingRe     = regexp.MustCompile(`\A\n# Role: [a-z0-9-]+\n\n`)
	clineFrontmatterRe     = regexp.MustCompile(`(?s)\A---\n(.*?)\n---\n(.*)\z`)
	clinePackagedNoteRe    = regexp.MustCompile(`(?ms)^> Packaged suite note: .*?do not look for the source checkout\.\n\n?`)
	clineInlineRefLinkRe   = regexp.MustCompile(`\[(?:references/)?([a-zA-Z0-9_-]+\.md)\]\((?:references/)?([a-zA-Z0-9_-]+\.md)\)`)
	clineLeakRosterRe      = regexp.MustCompile(`roster/[a-zA-Z0-9_/.<>-]+`)
	clineLeakRelativeRe    = regexp.MustCompile(`\.\./[a-zA-Z0-9_./<>-]+`)
	clineFailLoudExemptSet = map[string]bool{"application-engineer": true}
)

// clineLeaks reports source-repo-relative references left in body.
//
// Two patterns rather than the Python's single alternation: that one uses a
// negative lookbehind, `(?<![\w.])\.\./...`, which RE2 does not support. The
// lookbehind is applied here by inspecting the preceding byte, which is what
// it meant -- a `../` that follows a word character or a dot is part of a
// longer token, not a relative path of ours.
func clineLeaks(body string) []string {
	var found []string
	found = append(found, clineLeakRosterRe.FindAllString(body, -1)...)
	for _, location := range clineLeakRelativeRe.FindAllStringIndex(body, -1) {
		if location[0] > 0 {
			previous := body[location[0]-1]
			isWord := previous == '_' || previous == '.' ||
				(previous >= 'a' && previous <= 'z') ||
				(previous >= 'A' && previous <= 'Z') ||
				(previous >= '0' && previous <= '9')
			if isWord {
				continue
			}
		}
		found = append(found, body[location[0]:location[1]])
	}
	return found
}

func clineParseFrontmatter(text string) (map[string]string, string, error) {
	match := clineFrontmatterRe.FindStringSubmatch(text)
	if match == nil {
		limit := 200
		if len(text) < limit {
			limit = len(text)
		}
		return nil, "", fmt.Errorf("could not parse frontmatter from:\n%q", text[:limit])
	}
	fields := map[string]string{}
	for _, line := range strings.Split(match[1], "\n") {
		key, value, _ := strings.Cut(line, ":")
		fields[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return fields, match[2], nil
}

// clineModelTiers maps a catalog tier onto the tier a Cline preset carries,
// read from the runner-capability manifest rather than listed here so the two
// cannot fall out of sync.
func clineModelTiers(repoRoot string) (map[string]string, error) {
	path := filepath.Join(repoRoot, "roster", "runner-capabilities.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: cannot read the runner-capability manifest: %w", path, err)
	}
	var manifest struct {
		ModelTiers map[string]struct {
			ClineTier string `json:"cline_tier"`
		} `json:"model_tiers"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(manifest.ModelTiers) == 0 {
		return nil, fmt.Errorf("%s: 'model_tiers' must be a non-empty object", path)
	}
	tiers := map[string]string{}
	for tier, data := range manifest.ModelTiers {
		if data.ClineTier == "" {
			return nil, fmt.Errorf("%s: model_tiers[%q] must declare 'cline_tier'", path, tier)
		}
		tiers[tier] = data.ClineTier
	}
	return tiers, nil
}

func clineFixInlineReferenceLinks(text string) string {
	return clineInlineRefLinkRe.ReplaceAllStringFunc(text, func(match string) string {
		groups := clineInlineRefLinkRe.FindStringSubmatch(match)
		// The Python uses a backreference so the two halves must be equal.
		// RE2 has none, so it is checked here instead.
		if groups[1] != groups[2] {
			return match
		}
		return fmt.Sprintf("the %q section below", "Reference: "+groups[1])
	})
}

func clineConvertAgentBody(role, body string) (string, error) {
	body = clineRoleHeadingRe.ReplaceAllString(body, "\n")
	for _, override := range clineRoleOverrides[role] {
		if !strings.Contains(body, override.from) {
			return "", fmt.Errorf("%s: expected override text not found: %q", role, override.from)
		}
		body = strings.Replace(body, override.from, override.to, 1)
	}
	body = clineRoutingConfigRe.ReplaceAllString(body, "this project's routing configuration's")
	for _, pair := range clinePathSubstitutions {
		body = strings.ReplaceAll(body, pair.from, pair.to)
	}
	body = clineSharedPolicyRe.ReplaceAllString(body, "# Shared policy: ")
	if role == "application-engineer" {
		body = strings.TrimRight(body, "\n") + clineApplicationEngineerNote + "\n"
	}
	return body, nil
}

func clineConvertAgentFile(sourcePath, role string, tiers map[string]string) (string, error) {
	raw, err := os.ReadFile(sourcePath)
	if err != nil {
		return "", err
	}
	fields, body, err := clineParseFrontmatter(string(raw))
	if err != nil {
		return "", err
	}
	var allowed []string
	seen := map[string]bool{}
	for _, tool := range strings.Split(fields["tools"], ",") {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			continue
		}
		mapped, known := clineToolMap[tool]
		if !known {
			return "", fmt.Errorf("%s: unknown tool %q", role, tool)
		}
		if !seen[mapped] {
			seen[mapped] = true
			allowed = append(allowed, mapped)
		}
	}
	tier, known := tiers[fields["model"]]
	if !known {
		return "", fmt.Errorf("%s: unknown model tier %q", role, fields["model"])
	}

	body, err = clineConvertAgentBody(role, body)
	if err != nil {
		return "", err
	}
	if !clineFailLoudExemptSet[role] {
		if leaks := clineLeaks(body); len(leaks) > 0 {
			return "", fmt.Errorf("%s: unrecognized source-repo-relative reference left in "+
				"ported body: %q -- add a rule to clinePathSubstitutions (or a role-specific "+
				"override) rather than shipping a leaked path", role, leaks[0])
		}
	}

	frontmatter := strings.Join([]string{
		"---",
		"name: " + fields["name"],
		`description: "` + fields["description"] + `"`,
		"modelTier: " + tier,
		"allowedTools: [" + strings.Join(allowed, ", ") + "]",
		"canonicalSource: " + fields["canonical_source"],
		"convertedFrom: agents/" + role + ".md",
		"---",
		"",
	}, "\n")
	return frontmatter + body, nil
}

func clineConvertSkillBody(name, body string) (string, error) {
	if !clinePackagedNoteRe.MatchString(body) {
		return "", fmt.Errorf("%s: expected 'Packaged suite note' callout not found", name)
	}
	replaced := false
	body = clinePackagedNoteRe.ReplaceAllStringFunc(body, func(match string) string {
		if replaced {
			return match
		}
		replaced = true
		return clinePackagingNote
	})
	body = clineFixInlineReferenceLinks(body)
	for _, pair := range clineSkillSubstitutions {
		body = strings.ReplaceAll(body, pair.from, pair.to)
	}
	return body, nil
}

func clineCheckSkillLeaks(name, body string) error {
	for _, leak := range clineLeaks(body) {
		exempt := false
		for _, allowed := range clineSkillLeakAllowlist {
			if strings.Contains(leak, allowed) || strings.Contains(allowed, leak) {
				exempt = true
				break
			}
		}
		if !exempt {
			return fmt.Errorf("%s: unrecognized source-repo-relative reference left in "+
				"ported skill: %q -- add a rule to clineSkillSubstitutions rather than "+
				"shipping a leaked path", name, leak)
		}
	}
	return nil
}

// PortClineAgents writes the Cline mirror under root/cline-agents/, reading
// the generated agents/ and skills/ from source.
func PortClineAgents(repoRoot, root, source string) (agents, skills []string, err error) {
	tiers, err := clineModelTiers(repoRoot)
	if err != nil {
		return nil, nil, err
	}
	if source == "" {
		source = root
	}

	agentTarget := filepath.Join(root, "cline-agents", "agents")
	entries, err := filepath.Glob(filepath.Join(source, "agents", "*.md"))
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(entries)
	for _, sourcePath := range entries {
		role := strings.TrimSuffix(filepath.Base(sourcePath), ".md")
		content, err := clineConvertAgentFile(sourcePath, role, tiers)
		if err != nil {
			return nil, nil, err
		}
		if err := os.MkdirAll(agentTarget, 0o755); err != nil {
			return nil, nil, err
		}
		if err := os.WriteFile(filepath.Join(agentTarget, role+".md"), []byte(content), 0o644); err != nil {
			return nil, nil, err
		}
		agents = append(agents, role)
	}

	skillTarget := filepath.Join(root, "cline-agents", "skills")
	skillDirs, err := os.ReadDir(filepath.Join(source, "skills"))
	if err != nil {
		return nil, nil, err
	}
	for _, entry := range skillDirs {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		skillPath := filepath.Join(source, "skills", name, "SKILL.md")
		raw, err := os.ReadFile(skillPath)
		if err != nil {
			continue
		}
		fields, body, err := clineParseFrontmatter(string(raw))
		if err != nil {
			return nil, nil, err
		}
		body, err = clineConvertSkillBody(name, strings.TrimSpace(body))
		if err != nil {
			return nil, nil, err
		}

		references, _ := filepath.Glob(filepath.Join(source, "skills", name, "references", "*.md"))
		sort.Strings(references)
		for _, reference := range references {
			referenceRaw, err := os.ReadFile(reference)
			if err != nil {
				return nil, nil, err
			}
			referenceBody := clineFixInlineReferenceLinks(strings.TrimSpace(string(referenceRaw)))
			for _, pair := range clineSkillSubstitutions {
				referenceBody = strings.ReplaceAll(referenceBody, pair.from, pair.to)
			}
			body += "\n\n# Reference: " + filepath.Base(reference) + "\n\n" + referenceBody
		}

		if err := clineCheckSkillLeaks(name, body); err != nil {
			return nil, nil, err
		}
		frontmatter := strings.Join([]string{
			"---",
			"name: " + name,
			"description: " + fields["description"],
			"canonicalSource: skills/" + name + "/SKILL.md",
			"---",
			"",
		}, "\n")
		if err := os.MkdirAll(skillTarget, 0o755); err != nil {
			return nil, nil, err
		}
		if err := os.WriteFile(filepath.Join(skillTarget, name+".md"),
			[]byte(frontmatter+"\n"+body+"\n"), 0o644); err != nil {
			return nil, nil, err
		}
		skills = append(skills, name)
	}

	// Remove ports of skills no longer sourced here. Without this the port
	// only ever adds: a skill that moves to a sub-plugin leaves its old copy
	// behind forever, and cline-agents keeps advertising a skill nothing
	// generates.
	keep := map[string]bool{}
	for _, name := range skills {
		keep[name+".md"] = true
	}
	stale, _ := filepath.Glob(filepath.Join(skillTarget, "*.md"))
	sort.Strings(stale)
	for _, path := range stale {
		if !keep[filepath.Base(path)] {
			if err := os.Remove(path); err != nil {
				return nil, nil, err
			}
		}
	}
	return agents, skills, nil
}
