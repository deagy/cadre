package generators

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every catalog role and every skill reaches the packaged distribution.
//
// `cadre generate-plugin --check` proves the committed distribution is what
// the generator produces. It cannot prove the generator produces everything:
// a role the generator skips is absent from both sides and the comparison
// passes. The failure is silent in the way that matters -- someone installs
// the plugin and a specialist the catalog promises simply is not there.
//
// Checked against the committed distribution, which is what a GitHub-sourced
// marketplace actually serves.

func packagedRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join(repositoryRoot(t), "plugin")
	if _, err := os.Stat(filepath.Join(root, "agents")); err != nil {
		t.Skipf("no committed distribution here: %v", err)
	}
	return root
}

func TestEveryCatalogRoleHasAPackagedWrapper(t *testing.T) {
	root := repositoryRoot(t)
	definitions := catalogDefinitions(t, filepath.Join(root, "roster", "catalog.yaml"))
	if len(definitions) < 100 {
		t.Fatalf("read %d roles from the catalog; the parse is broken", len(definitions))
	}
	plugin := packagedRoot(t)

	var missing []string
	for agentID := range definitions {
		if _, err := os.Stat(filepath.Join(plugin, "agents", agentID+".md")); err != nil {
			missing = append(missing, agentID)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d catalog role(s) have no packaged wrapper: %v\n"+
			"--check would still pass: a role the generator skips is absent from "+
			"both the fresh build and the committed one.", len(missing), missing)
	}
}

func TestEveryPackagedWrapperNamesARoleTheCatalogDeclares(t *testing.T) {
	// The other direction, and the one that catches a rename. A wrapper for a
	// role the catalog dropped installs a specialist nothing routes to -- it
	// can never be selected, and it sits in the distribution looking current.
	root := repositoryRoot(t)
	definitions := catalogDefinitions(t, filepath.Join(root, "roster", "catalog.yaml"))
	plugin := packagedRoot(t)

	entries, err := os.ReadDir(filepath.Join(plugin, "agents"))
	if err != nil {
		t.Fatalf("reading the packaged agents: %v", err)
	}
	packaged := 0
	var unknown []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".md") {
			continue
		}
		packaged++
		agentID := strings.TrimSuffix(name, ".md")
		if _, declared := definitions[agentID]; !declared {
			unknown = append(unknown, agentID)
		}
	}
	if packaged == 0 {
		t.Fatal("no wrappers were read; this test would prove nothing")
	}
	if len(unknown) > 0 {
		t.Errorf("%d packaged wrapper(s) name a role the catalog does not declare: %v",
			len(unknown), unknown)
	}
}

func TestEverySkillReachesThePackage(t *testing.T) {
	// Skills package either to the distribution root or into a named
	// sub-plugin, and which one is decided by skillPackageTargets. A skill
	// mapped nowhere lands at the root; a skill mapped to a plugin that moved
	// lands nowhere at all, and nothing else would notice.
	root := repositoryRoot(t)
	skillsRoot := filepath.Join(root, ".agents", "skills")
	entries, err := os.ReadDir(skillsRoot)
	if err != nil {
		t.Skipf("no skills directory here: %v", err)
	}
	plugin := packagedRoot(t)

	checked := 0
	var missing []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if _, err := os.Stat(filepath.Join(skillsRoot, name, "SKILL.md")); err != nil {
			continue
		}
		checked++

		// The same rule the generator applies. skillPackageTargets holds the
		// destination directory itself -- "plugins/lifecycle/skills", already
		// including the skills/ segment -- so the skill name is joined to it
		// directly. Appending "skills" again produces skills/skills/, which is
		// what the first version of this test looked for and did not find.
		destination := "skills"
		if target, mapped := skillPackageTargets[name]; mapped {
			destination = target
		}
		relative := filepath.Join(destination, name, "SKILL.md")
		if _, err := os.Stat(filepath.Join(plugin, relative)); err != nil {
			missing = append(missing, name+" -> "+relative)
		}
	}
	if checked == 0 {
		t.Fatal("no skills were found; this test would prove nothing")
	}
	if len(missing) > 0 {
		t.Errorf("%d skill(s) did not reach the package: %v", len(missing), missing)
	}
}

func TestEverySkillPackageTargetNamesAPluginThatExists(t *testing.T) {
	// skillPackageTargets is a hand-maintained map from a skill to the
	// sub-plugin that owns it. A target naming a plugin that was renamed or
	// removed sends the skill somewhere nothing installs, and the skill simply
	// disappears from the distribution.
	plugin := packagedRoot(t)
	if len(skillPackageTargets) == 0 {
		t.Skip("no skill is mapped to a sub-plugin")
	}
	for skill, target := range skillPackageTargets {
		if _, err := os.Stat(filepath.Join(plugin, filepath.FromSlash(target))); err != nil {
			t.Errorf("skill %q is mapped to %q, which is not in the distribution: %v",
				skill, target, err)
		}
	}
}
