package generators

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// Section-granular shared-policy excerpting, ported from
// generate_global_plugin.py's split_policy_sections()/
// excerpt_universal_sections().
//
// A role whose capability tier is not write-capable receives a policy file's
// preamble (which carries the file's own applicability header) plus exactly
// the sections that bind every tier, in file order. Every other role receives
// the file verbatim. Every failure mode below is fail-closed on purpose: the
// alternative is silently shipping a read-only wrapper whose universally
// binding rule was renamed out from under it, which looks like nothing
// happened.

// universalPolicySections maps a repository-relative shared-policy path to the
// exact `## ` headings (heading text, without the `## `) that bind every tier.
// Mirrors UNIVERSAL_POLICY_SECTIONS.
var universalPolicySections = map[string][]string{
	"roster/shared/workspace-isolation.md": {
		"Never mutate a working tree you did not create",
		"The security-relevant-resolver rule",
		"Never remove or prune a worktree yourself",
		"No runner names as behavioral conditions",
	},
}

// pySplitLines mirrors Python's str.splitlines() for the line endings these
// Markdown sources actually use: it drops the trailing empty element that
// strings.Split leaves behind on a newline-terminated file.
func pySplitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

type policySection struct {
	heading string
	text    string
}

// splitPolicySections splits a Markdown policy file into its preamble and its
// level-2 sections. Headings inside fenced code blocks are ignored -- these
// files contain shell samples. Both ``` and ~~~ fences count; an unbalanced
// fence is an error rather than silently swallowing every heading after it
// (which would truncate the excerpt with no other signal).
func splitPolicySections(body string) (string, []policySection, error) {
	var preamble []string
	type rawSection struct {
		heading string
		lines   []string
	}
	var sections []rawSection
	inFence := false
	fenceMarker := ""

	for _, line := range pySplitLines(body) {
		stripped := strings.TrimLeftFunc(line, unicode.IsSpace)
		for _, marker := range []string{"```", "~~~"} {
			if strings.HasPrefix(stripped, marker) && (!inFence || fenceMarker == marker) {
				inFence = !inFence
				if inFence {
					fenceMarker = marker
				} else {
					fenceMarker = ""
				}
				break
			}
		}
		switch {
		case !inFence && strings.HasPrefix(line, "## "):
			sections = append(sections, rawSection{
				heading: strings.TrimSpace(line[3:]),
				lines:   []string{line},
			})
		case len(sections) > 0:
			last := &sections[len(sections)-1]
			last.lines = append(last.lines, line)
		default:
			preamble = append(preamble, line)
		}
	}

	if inFence {
		return "", nil, fmt.Errorf(
			"unbalanced %q code fence: every heading after it would be swallowed into "+
				"the preceding section, silently truncating the excerpt", fenceMarker)
	}

	parsed := make([]policySection, 0, len(sections))
	for _, section := range sections {
		parsed = append(parsed, policySection{
			heading: section.heading,
			text:    strings.TrimSpace(strings.Join(section.lines, "\n")),
		})
	}
	return strings.TrimSpace(strings.Join(preamble, "\n")), parsed, nil
}

// excerptUniversalSections returns body reduced to its preamble plus the
// sections that bind every tier, for a role the file's own applicability
// header excludes.
func excerptUniversalSections(relative, body string) (string, error) {
	required, ok := universalPolicySections[relative]
	if !ok || len(required) == 0 {
		return "", fmt.Errorf(
			"%s: universalPolicySections declares no universally binding section; this "+
				"mechanism cannot be used to drop a whole file", relative)
	}

	preamble, sections, err := splitPolicySections(body)
	if err != nil {
		return "", fmt.Errorf("%s: %w", relative, err)
	}
	if preamble == "" {
		return "", fmt.Errorf(
			"%s: no preamble above the first '## ' heading, so the excerpt would carry "+
				"no applicability header", relative)
	}

	headings := make([]string, 0, len(sections))
	headingSet := map[string]bool{}
	for _, section := range sections {
		headings = append(headings, section.heading)
		headingSet[section.heading] = true
	}

	// A *balanced* stray fence pair needs no parser bug to leak policy: it
	// deletes the section boundaries between its markers, and whatever headings
	// it swallows are absorbed into the preceding section. When that section is
	// one of the kept universal ones, write-capable-only text ships to every
	// read-only wrapper with no other signal.
	rawHeadingCount := 0
	for _, line := range pySplitLines(body) {
		if strings.HasPrefix(line, "## ") {
			rawHeadingCount++
		}
	}
	if rawHeadingCount != len(sections) {
		return "", fmt.Errorf(
			"%s: parsed %d section(s) but the file has %d '## ' line(s). A code fence is "+
				"swallowing a section boundary, which silently merges sections and can leak "+
				"write-capable-only text into the read-only excerpt",
			relative, len(sections), rawHeadingCount)
	}

	requiredSet := map[string]bool{}
	for _, heading := range required {
		requiredSet[heading] = true
	}
	var missing []string
	for _, heading := range required {
		if !headingSet[heading] {
			missing = append(missing, heading)
		}
	}
	if len(missing) > 0 {
		return "", fmt.Errorf(
			"%s: required universally binding section(s) not found: %s. Found: %s. A "+
				"heading listed in universalPolicySections was renamed or removed -- update "+
				"both together", relative, strings.Join(missing, ", "), strings.Join(headings, ", "))
	}

	// The file's own applicability header must enumerate exactly the sections
	// this table names, so a reader of either can check it against the other.
	// Matched against the header's bullet list, not the whole preamble text.
	promised := map[string]bool{}
	for _, line := range pySplitLines(preamble) {
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		candidate := strings.TrimSpace(line[2:])
		if headingSet[candidate] {
			promised[candidate] = true
		}
	}
	var unlisted, overpromised []string
	for heading := range requiredSet {
		if !promised[heading] {
			unlisted = append(unlisted, heading)
		}
	}
	for heading := range promised {
		if !requiredSet[heading] {
			overpromised = append(overpromised, heading)
		}
	}
	if len(unlisted) > 0 || len(overpromised) > 0 {
		sort.Strings(unlisted)
		sort.Strings(overpromised)
		var detail []string
		if len(unlisted) > 0 {
			detail = append(detail, "registered but not named in the header's list: "+strings.Join(unlisted, ", "))
		}
		if len(overpromised) > 0 {
			detail = append(detail, "named in the header's list but not registered, so it would be "+
				"dropped from every read-only wrapper despite the header saying it binds every "+
				"tier: "+strings.Join(overpromised, ", "))
		}
		return "", fmt.Errorf(
			"%s: the applicability header and universalPolicySections disagree about which "+
				"sections bind every tier (%s)", relative, strings.Join(detail, "; "))
	}

	kept := []string{preamble}
	for _, section := range sections {
		if !requiredSet[section.heading] {
			continue
		}
		_, after, _ := strings.Cut(section.text, "\n")
		if strings.TrimSpace(after) == "" {
			return "", fmt.Errorf(
				"%s: universally binding section %q has an empty body; the excerpt would "+
					"ship the heading alone", relative, section.heading)
		}
		kept = append(kept, section.text)
	}
	return strings.Join(kept, "\n\n"), nil
}
