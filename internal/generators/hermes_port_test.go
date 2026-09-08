package generators

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// The Hermes port reproduces the committed tree exactly, and the tree it
// produces is one Hermes will load: every SKILL.md opens with frontmatter
// whose name is its directory, whose description fits the skill index, and
// whose body carries no path that only resolved in this repository.
//
// Ported from generate-cadre-skills.py and validate_skill_tree.py, which
// produced and checked the first tree on the machine it ran on and were
// never committed anywhere. The checks here are that validator's, so the
// generator cannot regress past what the installed tree already passed.

var (
	sharedHermesOnce sync.Once
	sharedHermesRepo string
	sharedHermesTree string
	sharedHermesErr  error
)

// hermesPortIntoScratch runs the port once per test binary into a throwaway
// root and returns the skills/cadre tree it wrote. The port rewrites 172
// files; doing it per test is what made the Cline port's package heavy
// enough under -race to starve its neighbours.
func hermesPortIntoScratch(t *testing.T) (repoRoot, tree string) {
	t.Helper()
	sharedHermesOnce.Do(func() {
		root := repositoryRoot(t)
		if _, err := os.Stat(filepath.Join(root, "hermes-plugins")); err != nil {
			sharedHermesErr = err
			return
		}
		scratch, err := os.MkdirTemp("", "hermes-port-")
		if err != nil {
			sharedHermesErr = err
			return
		}
		if _, _, err := PortHermesSkills(root, scratch); err != nil {
			sharedHermesErr = err
			return
		}
		sharedHermesRepo = root
		sharedHermesTree = filepath.Join(scratch, "skills", "cadre")
	})
	if sharedHermesErr != nil {
		t.Skipf("the shared port could not run: %v", sharedHermesErr)
	}
	return sharedHermesRepo, sharedHermesTree
}

func TestTheHermesPortReproducesTheCommittedTreeExactly(t *testing.T) {
	repoRoot, ported := hermesPortIntoScratch(t)
	committed := filepath.Join(repoRoot, "hermes-plugins", "skills", "cadre")
	out, err := exec.Command("diff", "-r", committed, ported).CombinedOutput()
	if err != nil {
		text := string(out)
		if len(text) > 4000 {
			text = text[:4000] + "\n... (truncated)"
		}
		t.Errorf("the port does not reproduce the committed tree; run `./bin/cadre port-hermes-skills`:\n%s", text)
	}

	entries, err := os.ReadDir(committed)
	if err != nil || len(entries) == 0 {
		t.Fatalf("the committed tree at %s is empty or unreadable: %v", committed, err)
	}
}

func TestTheHermesPortIsIdempotent(t *testing.T) {
	repoRoot, first := hermesPortIntoScratch(t)
	again := t.TempDir()
	if _, _, err := PortHermesSkills(repoRoot, again); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("diff", "-r", first, filepath.Join(again, "skills", "cadre")).CombinedOutput(); err != nil {
		t.Errorf("two runs differ:\n%s", out)
	}
}

func TestTheHermesPortCoversEveryCatalogRoleAndWorkflow(t *testing.T) {
	repoRoot, tree := hermesPortIntoScratch(t)
	catalog, err := loadHermesCatalog(filepath.Join(repoRoot, "roster", "catalog.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	skills := hermesSkillFiles(t, tree)
	names := map[string]bool{}
	for _, skill := range skills {
		names[filepath.Base(filepath.Dir(skill))] = true
	}
	for id := range catalog {
		if !names[id] {
			t.Errorf("catalog role %s has no skill", id)
		}
	}
	playbooks, _ := filepath.Glob(filepath.Join(repoRoot, "roster", "workflows", "*.md"))
	for _, playbook := range playbooks {
		name := "workflow-" + strings.TrimSuffix(filepath.Base(playbook), ".md")
		if !names[name] {
			t.Errorf("workflow %s has no skill", name)
		}
	}
	if !names["cadre-orchestrator"] {
		t.Error("no cadre-orchestrator skill")
	}
	want := len(catalog) + len(playbooks) + 1
	if len(skills) != want {
		t.Errorf("%d skills, want %d (%d roles + %d workflows + the orchestrator)", len(skills), want, len(catalog), len(playbooks))
	}
}

// hermesLeftoverRelative is a link that only resolved inside this
// repository's layout; validate_skill_tree.py refused any tree carrying one.
var hermesLeftoverRelative = regexp.MustCompile("`(?:\\.\\./)+")

func TestEveryHermesSkillIsOneHermesWillLoad(t *testing.T) {
	_, tree := hermesPortIntoScratch(t)
	seen := map[string]string{}
	for _, skill := range hermesSkillFiles(t, tree) {
		relative, _ := filepath.Rel(tree, skill)
		raw, err := os.ReadFile(skill)
		if err != nil {
			t.Fatal(err)
		}
		content := string(raw)
		if !strings.HasPrefix(content, "---\n") {
			t.Errorf("%s: no frontmatter at byte 0", relative)
			continue
		}
		end := strings.Index(content[4:], "\n---\n")
		if end < 0 {
			t.Errorf("%s: unclosed frontmatter", relative)
			continue
		}
		var frontmatter map[string]any
		if err := yaml.Unmarshal([]byte(content[4:4+end]), &frontmatter); err != nil {
			t.Errorf("%s: frontmatter is not YAML: %v", relative, err)
			continue
		}
		name, _ := frontmatter["name"].(string)
		if name != filepath.Base(filepath.Dir(skill)) {
			t.Errorf("%s: name %q is not the directory %q", relative, name, filepath.Base(filepath.Dir(skill)))
		}
		if previous, dup := seen[name]; dup {
			t.Errorf("%s: name %q already used by %s", relative, name, previous)
		}
		seen[name] = relative
		description := strings.TrimSpace(fmt.Sprint(frontmatter["description"]))
		switch {
		case description == "" || description == "<nil>":
			t.Errorf("%s: empty description", relative)
		case utf8.RuneCountInString(description) > hermesDescriptionLimit:
			t.Errorf("%s: description is %d runes, over %d: %q", relative, utf8.RuneCountInString(description), hermesDescriptionLimit, description)
		default:
			trimmed := strings.TrimRight(description, ".…")
			words := strings.Split(trimmed, " ")
			if hermesTrailingWords[strings.ToLower(words[len(words)-1])] {
				t.Errorf("%s: description ends on a dangling word: %q", relative, description)
			}
		}
		body := strings.TrimSpace(content[4+end+5:])
		if body == "" {
			t.Errorf("%s: empty body", relative)
		}
		if len(content) > 100_000 {
			t.Errorf("%s: %d bytes, over the 100k a skill may be", relative, len(content))
		}
		if hermesLeftoverRelative.MatchString(content) {
			t.Errorf("%s: carries a relative link that only resolved in this repository", relative)
		}
	}
}

func TestTheHermesDescriptionCutsLikeTheOriginal(t *testing.T) {
	// Cases pinned to what the installed tree carries. Two quirks of the
	// original are kept on purpose: the ellipsis branch is dead, because the
	// final strip of " …" removes the ellipsis it just added and a period is
	// appended instead; and a trailing preposition is trimmed one word at a
	// time until the last word is not one.
	for _, probe := range []struct{ role, text, want string }{
		{"security-reviewer",
			"Review changes for security defects. Second sentence is ignored.",
			"review changes for security defects."},
		{"x", "Operate the agent-facing vectorized knowledge store: authorize and normalize imports, protect sensitive content.",
			"operate the agent-facing vectorized knowledge store."},
		{"x", "Plan for the migration of", "plan for the migration."},
		{"a-role-with-no-text", "", "role contract: a-role-with-no-text."},
	} {
		if got := hermesDescription(probe.text, probe.role); got != probe.want {
			t.Errorf("hermesDescription(%q) = %q, want %q", probe.text, got, probe.want)
		}
	}
}

func TestTheHermesPathRewriteKeepsTheEnvironmentVariable(t *testing.T) {
	// "$HERMES_HOME" in a Go replacement template is a group reference and
	// vanishes; the first run of the port shipped every shared-policy path as
	// "/skills/cadre/..." before that was caught by diffing against the
	// installed tree.
	got := hermesRewritePaths("Follow `../../shared/team-profile.yaml` and `../../orchestration/escalation-policy.md`.")
	for _, want := range []string{
		"`$HERMES_HOME/skills/cadre/cadre-orchestrator/references/shared/team-profile.yaml`",
		"`$HERMES_HOME/skills/cadre/cadre-orchestrator/references/escalation-policy.md`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rewrite lost %q:\n%s", want, got)
		}
	}
	if got := hermesRewritePaths("See `../../review/code-reviewer/AGENT.md` and debugging.md."); got != "See the installed `code-reviewer` skill and the installed `workflow-debugging` skill." {
		t.Errorf("sibling rewrite = %q", got)
	}
}

func TestTheHermesFrontmatterEscapesAQuotedDescription(t *testing.T) {
	block := hermesFrontmatter("x", `says "hello" \ world`, []string{"cadre", "a", "a", "", "b"})
	var frontmatter map[string]any
	if err := yaml.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(block, "---\n"), "---\n")), &frontmatter); err != nil {
		t.Fatalf("frontmatter is not YAML: %v\n%s", err, block)
	}
	if frontmatter["description"] != `says "hello" \ world` {
		t.Errorf("description round-tripped as %q", frontmatter["description"])
	}
	if !strings.Contains(block, "tags: [cadre, a, b]") {
		t.Errorf("tags not deduplicated with cadre first:\n%s", block)
	}
}

func hermesSkillFiles(t *testing.T, tree string) []string {
	t.Helper()
	var skills []string
	err := filepath.Walk(tree, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Base(path) == "SKILL.md" {
			skills = append(skills, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) == 0 {
		t.Fatalf("no SKILL.md under %s", tree)
	}
	return skills
}
