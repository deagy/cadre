package kernel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Init: what this kernel owes, stated without reference to any other
// implementation.
//
// Split out of init_differential_test.go, which compares this kernel against the
// Python one and goes when it does. These say what the behaviour *is*, and
// stay. Keeping both in one file meant a whole-file deletion would have taken
// the second half with the first -- measured, that cost twenty points of
// coverage nobody intended to give up.

func TestInitInitialisesIntoABlockedState(t *testing.T) {
	// A freshly initialised project is not ready to run a lifecycle, and says
	// so. The alternative -- authorities defaulted to somebody, impacts
	// defaulted to "probably not applicable" -- is the kernel making exactly
	// the decisions it exists to make a human make.
	root := filepath.Join(t.TempDir(), "project")
	registry := NewRegistry()
	if err := registry.LoadProvider(providerManifest(t)); err != nil {
		t.Fatal(err)
	}
	result, err := registry.Initialize(InitRequest{
		Root: root, Profile: "secure-cloud", ProjectID: "probe",
		Classification: "internal", Runner: "both",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.values["ready"] != false {
		t.Error("a freshly initialised project reported itself ready")
	}
	if len(listOf(result.values["blockers"])) == 0 {
		t.Error("it is not ready and names no blocker")
	}

	authorities := decodeFile(t, filepath.Join(root, Overlay, "authorities.json"))
	for _, role := range AuthorityRoleOrder {
		authority, _ := authorities[role].(map[string]any)
		if authority == nil {
			t.Errorf("%s is missing from the authority map", role)
			continue
		}
		if authority["status"] != "unknown" || authority["assignee"] != nil {
			t.Errorf("%s starts assigned: %v", role, authority)
		}
	}
	impact := decodeFile(t, filepath.Join(root, Overlay, "impact-profile.json"))
	if impact["status"] != "blocked" {
		t.Errorf("the impact profile starts %v", impact["status"])
	}
	// Each declared impact category starts unresolved and is named as a
	// blocker. This profile declares none, so the list is empty and the
	// correspondence is what is checked rather than the count.
	if len(listOf(impact["blocking_unknowns"])) != len(listOf(impact["impact_categories"])) {
		t.Errorf("%d impact categories but %d blockers",
			len(listOf(impact["impact_categories"])), len(listOf(impact["blocking_unknowns"])))
	}
	for _, raw := range listOf(impact["impact_categories"]) {
		category, _ := raw.(map[string]any)
		if category["applicability"] != "unknown" {
			t.Errorf("an impact category starts %v rather than unknown", category["applicability"])
		}
	}
}

func TestInitNeverOverwrites(t *testing.T) {
	// The property a re-run depends on. Everything already there is left
	// exactly as it is, including a wrapper somebody has tailored.
	root := filepath.Join(t.TempDir(), "project")
	registry := NewRegistry()
	if err := registry.LoadProvider(providerManifest(t)); err != nil {
		t.Fatal(err)
	}
	request := InitRequest{
		Root: root, Profile: "secure-cloud", ProjectID: "probe",
		Classification: "internal", Runner: "both",
	}
	if _, err := registry.Initialize(request); err != nil {
		t.Fatal(err)
	}
	writeText(t, filepath.Join(root, ".claude", "agents", "code-reviewer.md"), "tailored")
	mutateJSON(t, filepath.Join(root, Overlay, "authorities.json"),
		func(document map[string]any) {
			authority, _ := document["product_owner"].(map[string]any)
			authority["status"] = "assigned"
		})
	before := walkFiles(t, root)

	result, err := registry.Initialize(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(listOf(result.values["created"])) != 0 {
		t.Errorf("the second init reported creating %v", result.values["created"])
	}
	after := walkFiles(t, root)
	for name, content := range before {
		if after[name] != content {
			t.Errorf("%s was rewritten by a second init", name)
		}
	}
}

func TestADryRunWritesNothingAtAll(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	if err := registry.LoadProvider(providerManifest(t)); err != nil {
		t.Fatal(err)
	}
	result, err := registry.Initialize(InitRequest{
		Root: root, Profile: "secure-cloud", ProjectID: "probe",
		Classification: "internal", Runner: "both", DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.values["mutation"] != false {
		t.Error("the dry run reported a mutation")
	}
	if len(listOf(result.values["would_create"])) == 0 {
		t.Fatal("the dry run would create nothing; this test would prove nothing")
	}
	if files := walkFiles(t, root); len(files) != 0 {
		t.Errorf("the dry run wrote %d files", len(files))
	}
}

func TestAnIncompleteManagedBlockIsRefused(t *testing.T) {
	// A start marker with no end has had something done to it that this
	// renderer cannot reason about. Guessing where the block ends would delete
	// whatever the project wrote after it.
	if _, err := RenderAgentsMarkdown("# Rules\n" + managedBlockStart + "\nhalf a block\n"); err == nil {
		t.Error("an unterminated managed block was accepted")
	}
	if _, err := RenderAgentsMarkdown("# Rules\n" + managedBlockEnd + "\n"); err == nil {
		t.Error("a stray end marker was accepted")
	}

	// And the ordinary cases still work, or the check above passes by
	// refusing everything.
	rendered, err := RenderAgentsMarkdown("# Rules\n\nKeep this.\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "Keep this.") || !strings.Contains(rendered, managedBlockStart) {
		t.Errorf("the project's own content or the managed block went missing:\n%s", rendered)
	}
	replaced, err := RenderAgentsMarkdown(
		"before\n\n" + managedBlockStart + "\nstale\n" + managedBlockEnd + "\n\nafter\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(replaced, "stale") {
		t.Error("the old managed block survived")
	}
	if !strings.Contains(replaced, "before") || !strings.Contains(replaced, "after") {
		t.Errorf("content around the block was lost:\n%s", replaced)
	}
}

func decodeFile(t *testing.T, path string) map[string]any {
	t.Helper()
	object, err := loadJSONObject(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return object
}

func writeText(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func walkFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !info.Mode().IsRegular() {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[relative] = string(data)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("walking %s: %v", root, err)
	}
	return files
}
