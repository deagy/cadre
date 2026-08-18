package generators

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The three lifecycle plugins duplicate content deliberately, and nothing
// about that arrangement makes the copies stay equal.
//
// cadre-lifecycle-core, -github and -gitlab are each self-sufficient: none may
// depend on another being installed, so three skills and the kernel bootstrap
// exist as full copies rather than shared imports.
//
// This guard exists because they demonstrably drifted. A change correcting
// "clone the archived deagy/agentic-sdlc" to "the in-tree kernel/" landed in
// lifecycle-onboarding only; both forge copies kept telling users to clone a
// repository that no longer exists. Each copy carried a "Duplication note"
// asserting a test enforced their sync -- naming a file that had been deleted,
// so the claim was false and nothing caught the divergence.
//
// Ported from plugin/tools/test_plugin_duplication_health.py.

// skillTriples are the (core, github, gitlab) copies of each duplicated skill.
// These are exactly the skills carrying a Duplication note callout, which
// TestEveryDuplicatedSkillIsCoveredByATriple keeps true, so a newly duplicated
// skill cannot be added without landing here too.
var skillTriples = map[string][3]string{
	"brief-pending-gates": {
		"lifecycle/skills/brief-pending-gates",
		"lifecycle-github/skills/brief-pending-gates-github",
		"lifecycle-gitlab/skills/brief-pending-gates-gitlab",
	},
	"lifecycle-onboarding": {
		"lifecycle/skills/lifecycle-onboarding",
		"lifecycle-github/skills/lifecycle-onboarding-github",
		"lifecycle-gitlab/skills/lifecycle-onboarding-gitlab",
	},
	"lifecycle-review": {
		"lifecycle/skills/lifecycle-review",
		"lifecycle-github/skills/lifecycle-review-generic-github",
		"lifecycle-gitlab/skills/lifecycle-review-generic-gitlab",
	},
}

type divergentSection struct {
	triple  string
	heading string
}

// knownDivergentSections are the sections that genuinely differ in substance
// rather than vocabulary. Anything not listed must normalize to identical text
// across all three copies. Adding an entry is deliberate and reviewable;
// silently drifting a shared paragraph is what this stops.
var knownDivergentSections = map[divergentSection]string{
	{"lifecycle-onboarding", "## Step 4 — Authorities: resolve what's needed now, defer the rest"}: "GitHub's login lookup is exact-match; GitLab's can return multiple " +
		"matches. Only the GitLab copy has a `gitlab-user-ambiguous` case to " +
		"explain, and each copy names its own forge-write skill as the consumer " +
		"of that reason-code vocabulary.",
	{"lifecycle-onboarding", "## Resolving a deferred authority later"}: "The forge plugins ship two review skills -- a generic one and a " +
		"PR/MR-backed one -- so their copies name both and say which to reach " +
		"for; the core plugin ships only lifecycle-review, so it names one. " +
		"Suffix normalization collapses both forge names onto the same token, " +
		"which is why the extra clause is what diverges rather than the name.",
}

var (
	duplicatedSkillFrontmatter = regexp.MustCompile(`(?s)\A---\n.*?\n---\n`)
	duplicationNote            = regexp.MustCompile(`(?m)^> (?:Duplication|Packaged suite) note:.*$`)
	sectionHeading             = regexp.MustCompile(`(?m)^##\s+.*$`)
	genericForge               = regexp.MustCompile(`-generic-(?:github|gitlab)\b`)
	forgeSuffix                = regexp.MustCompile(`-(?:github|gitlab)\b`)
	forgeName                  = regexp.MustCompile(`\bGitHub\b|\bGitLab\b`)
	forgeNameLower             = regexp.MustCompile(`\bgithub\b|\bgitlab\b`)
	forgeCLI                   = regexp.MustCompile("`gh`|`glab`")
	reviewRequest              = regexp.MustCompile(`\bpull request\b|\bmerge request\b`)
	reviewRequestAbb           = regexp.MustCompile(`\bPR\b|\bMR\b`)
	collapseSpace              = regexp.MustCompile(`\s+`)
)

// normalizeForgeVocabulary strips per-copy boilerplate and maps both forges'
// vocabulary onto shared placeholders, so only substantive differences survive.
//
// Both forges normalize to the *same* token deliberately: a copy that says
// GitHub where its sibling says GitLab is expected, but a copy that says
// GitHub where its sibling says something else entirely is drift.
func normalizeForgeVocabulary(text string) string {
	text = duplicatedSkillFrontmatter.ReplaceAllString(text, "")
	text = duplicationNote.ReplaceAllString(text, "")
	// Forge-specific skill names, before the bare-token rules chew them up.
	text = strings.ReplaceAll(text, "create-github-gate-issues", "FORGE_GATE_ISSUES_SKILL")
	text = strings.ReplaceAll(text, "gitlab-gate-tracking", "FORGE_GATE_ISSUES_SKILL")
	text = genericForge.ReplaceAllString(text, "")
	text = forgeSuffix.ReplaceAllString(text, "")
	text = forgeName.ReplaceAllString(text, "FORGE")
	text = forgeNameLower.ReplaceAllString(text, "forge")
	text = forgeCLI.ReplaceAllString(text, "`FORGECLI`")
	text = reviewRequest.ReplaceAllString(text, "review request")
	text = reviewRequestAbb.ReplaceAllString(text, "RR")
	return text
}

// splitSections maps `## heading` to its body, with anything before the first
// heading under <preamble>.
//
// Whitespace is collapsed here rather than in normalization, which has to
// leave line structure intact for the heading pattern to see. Reflowing a
// paragraph is not drift.
func splitSections(text string) map[string]string {
	headings := sectionHeading.FindAllStringIndex(text, -1)
	raw := map[string]string{}
	if len(headings) == 0 {
		raw["<preamble>"] = text
	} else {
		raw["<preamble>"] = text[:headings[0][0]]
		for index, location := range headings {
			end := len(text)
			if index+1 < len(headings) {
				end = headings[index+1][0]
			}
			raw[strings.TrimSpace(text[location[0]:location[1]])] = text[location[1]:end]
		}
	}
	sections := make(map[string]string, len(raw))
	for heading, body := range raw {
		sections[heading] = strings.TrimSpace(collapseSpace.ReplaceAllString(body, " "))
	}
	return sections
}

func pluginsRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(repositoryRoot(t), "plugin", "plugins")
}

func readDuplicatedSkill(t *testing.T, relative string) string {
	t.Helper()
	path := filepath.Join(pluginsRoot(t), filepath.FromSlash(relative), "SKILL.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing duplicated skill copy %s: %v", relative, err)
	}
	return string(content)
}

func headingsOf(sections map[string]string) []string {
	names := make([]string, 0, len(sections))
	for heading := range sections {
		names = append(names, heading)
	}
	sort.Strings(names)
	return names
}

func TestDuplicatedSkillBodiesStayInSync(t *testing.T) {
	for id, triple := range skillTriples {
		reference := splitSections(normalizeForgeVocabulary(readDuplicatedSkill(t, triple[0])))
		for _, name := range []struct {
			label    string
			relative string
		}{{"github", triple[1]}, {"gitlab", triple[2]}} {
			sections := splitSections(normalizeForgeVocabulary(readDuplicatedSkill(t, name.relative)))
			if strings.Join(headingsOf(reference), "\n") != strings.Join(headingsOf(sections), "\n") {
				t.Errorf("%s: the core and %s copies have different section headings -- "+
					"a section was added, removed or renamed in one copy only.\n"+
					"  core:   %v\n  %s: %v", id, name.label,
					headingsOf(reference), name.label, headingsOf(sections))
				continue
			}
			for heading, body := range reference {
				if _, exempt := knownDivergentSections[divergentSection{id, heading}]; exempt {
					continue
				}
				if body != sections[heading] {
					t.Errorf("%s: section %q differs between the core and %s copies "+
						"after forge-vocabulary normalization. Propagate the change to "+
						"every copy, or -- if the difference is genuinely "+
						"forge-specific -- add it to knownDivergentSections with a "+
						"reason.", id, heading, name.label)
				}
			}
		}
	}
}

func TestKnownDivergentSectionsStillExist(t *testing.T) {
	// An exemption for a section that no longer exists is dead weight that
	// would silently keep exempting a future section of the same name.
	for exemption, reason := range knownDivergentSections {
		triple, known := skillTriples[exemption.triple]
		if !known {
			t.Errorf("exemption names an unknown skill triple: %q", exemption.triple)
			continue
		}
		if strings.TrimSpace(reason) == "" {
			t.Errorf("%s/%s is exempt without a reason", exemption.triple, exemption.heading)
		}
		sections := splitSections(normalizeForgeVocabulary(readDuplicatedSkill(t, triple[0])))
		if _, present := sections[exemption.heading]; !present {
			t.Errorf("knownDivergentSections exempts %q from %s, but no such section "+
				"exists -- remove the stale exemption", exemption.heading, exemption.triple)
		}
	}
}

func TestKnownDivergentSectionsActuallyDiverge(t *testing.T) {
	// An exemption that is no longer needed silently disables the check for
	// that section. If the copies have converged, delete the entry.
	for exemption := range knownDivergentSections {
		bodies := map[string]bool{}
		for _, relative := range skillTriples[exemption.triple] {
			sections := splitSections(normalizeForgeVocabulary(readDuplicatedSkill(t, relative)))
			bodies[sections[exemption.heading]] = true
		}
		if len(bodies) < 2 {
			t.Errorf("%s: section %q is listed in knownDivergentSections but all "+
				"copies now agree -- remove the exemption so the section is checked "+
				"again", exemption.triple, exemption.heading)
		}
	}
}

func TestEveryDuplicatedSkillIsCoveredByATriple(t *testing.T) {
	// The Duplication note callout is the marker that a copy is meant to stay
	// in sync. A copy carrying that note but absent from skillTriples would
	// repeat exactly the failure this file exists to prevent.
	root := pluginsRoot(t)
	matches, err := filepath.Glob(filepath.Join(root, "*", "skills", "*", "SKILL.md"))
	if err != nil {
		t.Fatalf("globbing skills: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no packaged lifecycle skills found; this guard checked nothing")
	}
	covered := map[string]bool{}
	for _, triple := range skillTriples {
		for _, relative := range triple {
			covered[relative] = true
		}
	}
	for _, path := range matches {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if !strings.Contains(string(content), "Duplication note:") {
			continue
		}
		relative, _ := filepath.Rel(root, filepath.Dir(path))
		if !covered[filepath.ToSlash(relative)] {
			t.Errorf("%s carries a Duplication note but is not listed in "+
				"skillTriples, so nothing checks it for drift", filepath.ToSlash(relative))
		}
	}
}

func TestDuplicationNotesDoNotClaimUnenforcedChecks(t *testing.T) {
	// The notes previously cited a file that did not exist. Now that one does,
	// assert the citation stays accurate rather than reverting to a bare "keep
	// these in sync" with no mechanism behind it.
	const enforcer = "plugin_duplication_health_test.go"
	for _, triple := range skillTriples {
		for _, relative := range triple[1:] {
			body := readDuplicatedSkill(t, relative)
			if !strings.Contains(body, "Duplication note:") {
				t.Errorf("%s lost its Duplication note callout", relative)
				continue
			}
			if !strings.Contains(body, enforcer) {
				t.Errorf("%s's Duplication note does not name %s, the test that "+
					"enforces it. A note citing nothing is how the copies drifted "+
					"the first time.", relative, enforcer)
			}
		}
	}
}
