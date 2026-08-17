package generators

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Two repository-shape rules that nothing else holds.
//
//   - .agents/skills/ is canonical; .claude/skills/ carries a thin pointer per
//     skill so Claude Code discovers it. A skill added to one and not the
//     other is invisible to exactly one runner, and neither tree complains.
//   - roster/ must not carry its own copy of a lifecycle artifact the kernel
//     owns. A second copy parses, looks authoritative, and drifts.
//
// The second extends internal/kernel/independence_test.go, which covers the
// contract JSON. This covers the rest of the set the Python names.
//
// Ported from test_repository_health.py's
// test_claude_skill_pointers_match_the_canonical_codex_skill and
// test_suite_does_not_duplicate_lifecycle_authority.

// skillFrontmatter reads a SKILL.md's `---` block into key/value pairs.
//
// Deliberately not a YAML parse: the block is a flat name/description pair by
// convention, and the property being checked is that two files agree about it.
func skillFrontmatter(t *testing.T, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	parts := strings.SplitN(string(raw), "---", 3)
	if len(parts) < 3 {
		t.Fatalf("%s has no frontmatter block", path)
	}
	fields := map[string]string{}
	for _, line := range strings.Split(parts[1], "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		fields[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return fields
}

func TestEveryCanonicalSkillHasAClaudePointerThatAgreesWithIt(t *testing.T) {
	root := repositoryRoot(t)
	canonicalRoot := filepath.Join(root, ".agents", "skills")
	entries, err := os.ReadDir(canonicalRoot)
	if err != nil {
		t.Skipf("no canonical skills directory here: %v", err)
	}

	checked := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		canonical := filepath.Join(canonicalRoot, entry.Name(), "SKILL.md")
		if _, err := os.Stat(canonical); err != nil {
			continue
		}
		checked++
		t.Run(entry.Name(), func(t *testing.T) {
			pointer := filepath.Join(root, ".claude", "skills", entry.Name(), "SKILL.md")
			if _, err := os.Stat(pointer); err != nil {
				t.Fatalf("no Claude Code pointer for this skill: %v\n"+
					"Without it the skill exists for Codex and not for Claude Code, "+
					"and neither tree says so.", err)
			}
			canonicalFields := skillFrontmatter(t, canonical)
			pointerFields := skillFrontmatter(t, pointer)
			for _, field := range []string{"name", "description"} {
				if canonicalFields[field] == "" {
					t.Errorf("the canonical skill declares no %s", field)
					continue
				}
				if pointerFields[field] != canonicalFields[field] {
					t.Errorf("the pointer's %s disagrees with the canonical skill:\n"+
						"  canonical: %q\n  pointer:   %q\n"+
						"A runner picking a skill by description would pick differently "+
						"depending on which tree it read.",
						field, canonicalFields[field], pointerFields[field])
				}
			}
		})
	}
	if checked == 0 {
		t.Fatal("no canonical skills were found; this test would prove nothing")
	}
	t.Logf("checked %d skill pointers", checked)
}

func TestNoClaudePointerExistsWithoutACanonicalSkill(t *testing.T) {
	// The other direction. A pointer left behind after its skill was renamed
	// advertises something that cannot be loaded, and the failure surfaces at
	// whoever tries to use it rather than here.
	root := repositoryRoot(t)
	pointerRoot := filepath.Join(root, ".claude", "skills")
	entries, err := os.ReadDir(pointerRoot)
	if err != nil {
		t.Skipf("no Claude skills directory here: %v", err)
	}
	var orphans []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(pointerRoot, entry.Name(), "SKILL.md")); err != nil {
			continue
		}
		canonical := filepath.Join(root, ".agents", "skills", entry.Name(), "SKILL.md")
		if _, err := os.Stat(canonical); err != nil {
			orphans = append(orphans, entry.Name())
		}
	}
	sort.Strings(orphans)
	if len(orphans) > 0 {
		t.Errorf("%d Claude pointer(s) name a skill that does not exist: %v",
			len(orphans), orphans)
	}
}

func TestRosterCarriesNoLifecycleArtifactTheKernelOwns(t *testing.T) {
	// The kernel owns gate schemas, run-record validation and gate-authority
	// semantics. A copy under roster/ is not a second source of truth -- it is
	// a file that drifts, parses fine, and gives no sign which one is read.
	//
	// internal/kernel/independence_test.go covers the contract JSON; these are
	// the rest of the set.
	root := repositoryRoot(t)
	rosterRoot := filepath.Join(root, "roster")
	if _, err := os.Stat(rosterRoot); err != nil {
		t.Skipf("no roster tree here: %v", err)
	}

	forbidden := map[string]string{
		"quality-gates.md":       "the gate list is the kernel's, not the roster's",
		"run-record.schema.json": "run-record validation is kernel-owned",
		"gate-authority.md":      "gate-authority semantics are kernel-owned",
	}
	var strays []string
	err := filepath.WalkDir(rosterRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if reason, forbid := forbidden[entry.Name()]; forbid {
			relative, _ := filepath.Rel(root, path)
			strays = append(strays, filepath.ToSlash(relative)+" ("+reason+")")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking roster/: %v", err)
	}
	sort.Strings(strays)
	if len(strays) > 0 {
		t.Errorf("%d lifecycle artifact(s) duplicated under roster/:\n  %s",
			len(strays), strings.Join(strays, "\n  "))
	}
}
