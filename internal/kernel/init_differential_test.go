package kernel

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// `init`, compared with the Python kernel on two empty directories.
//
// Every file is compared, not a chosen few: the overlay's five documents, the
// version lock, AGENTS.md, and every generated runner wrapper. The wrappers
// are where this port was wrong twice -- once on the order agents are
// collected in, once on how a role definition's em-dash is escaped into TOML
// -- and neither showed up anywhere else.
//
// The two roots differ by name, so the root path is normalised out before
// comparison. Nothing else is.

type initCase struct {
	name string
	args []string
	// prepare runs against both roots before init, for the cases about what
	// init does with something already there.
	prepare func(t *testing.T, root string)
	// expectExit is asserted against both kernels.
	expectExit int
	// writesNothing marks the cases where an empty project is the correct
	// outcome -- a dry run, or a refusal -- so the vacuity check below does
	// not fire on them.
	writesNothing bool
}

var initCases = []initCase{
	{name: "a profile with a full role catalog", args: []string{
		"--profile", "secure-cloud", "--project-id", "probe"}},
	{name: "no profile at all", args: []string{"--project-id", "probe"}},
	{name: "kernel-only, named explicitly", args: []string{
		"--profile", "kernel-only", "--project-id", "probe"}},
	{name: "one runner instead of both", args: []string{
		"--profile", "secure-cloud", "--project-id", "probe", "--runner", "codex"}},
	{name: "the other runner", args: []string{
		"--profile", "secure-cloud", "--project-id", "probe", "--runner", "claude"}},
	{name: "a classification other than the default", args: []string{
		"--profile", "secure-cloud", "--project-id", "probe", "--classification", "restricted"}},
	{name: "a dry run writes nothing", args: []string{
		"--profile", "secure-cloud", "--project-id", "probe", "--dry-run"},
		writesNothing: true},

	{
		name:       "a profile nobody supplies",
		args:       []string{"--profile", "no-such-profile", "--project-id", "probe"},
		expectExit: 1, writesNothing: true,
	},
	{
		name:       "an extension nobody supplies",
		args:       []string{"--profile", "secure-cloud", "--project-id", "probe", "--extension", "no-such-extension"},
		expectExit: 1, writesNothing: true,
	},

	{
		// Re-running init is a no-op, not a refresh. An overlay accumulates
		// decisions -- who holds which authority, which impacts apply -- and
		// an init that rewrote them would discard the lot on a re-run somebody
		// meant as a check.
		name: "re-running over an existing overlay",
		args: []string{"--profile", "secure-cloud", "--project-id", "probe"},
		prepare: func(t *testing.T, root string) {
			initialiseWith(t, root, "--profile", "secure-cloud", "--project-id", "probe")
			// A decision recorded after the first init. If init overwrites, it
			// disappears -- and the file comparison says so.
			mutateJSON(t, filepath.Join(root, Overlay, "authorities.json"),
				func(document map[string]any) {
					authority, _ := document["product_owner"].(map[string]any)
					authority["status"] = "assigned"
					authority["assignee"] = "github.com/somebody"
				})
		},
	},
	{
		// AGENTS.md is the one file init modifies rather than creates, and the
		// project's own content around the managed block has to survive.
		name: "an AGENTS.md that already says something",
		args: []string{"--profile", "secure-cloud", "--project-id", "probe"},
		prepare: func(t *testing.T, root string) {
			writeText(t, filepath.Join(root, "AGENTS.md"),
				"# House rules\n\nAlways run the tests.\n")
		},
	},
	{
		name: "an AGENTS.md with a managed block already in it",
		args: []string{"--profile", "secure-cloud", "--project-id", "probe"},
		prepare: func(t *testing.T, root string) {
			writeText(t, filepath.Join(root, "AGENTS.md"),
				"# House rules\n\n"+managedBlockStart+"\nstale content\n"+managedBlockEnd+
					"\n\n## Afterwards\n\nKeep this too.\n")
		},
	},
	{
		// A wrapper somebody has edited. Init reports it as existing and
		// leaves it alone; re-generating it would silently discard the edit.
		name: "a runner wrapper that already exists",
		args: []string{"--profile", "secure-cloud", "--project-id", "probe"},
		prepare: func(t *testing.T, root string) {
			writeText(t, filepath.Join(root, ".claude", "agents", "code-reviewer.md"),
				"---\nname: code-reviewer\n---\n\nTailored by this project.\n")
		},
	},
}

func TestInitProducesIdenticalProjects(t *testing.T) {
	for _, probe := range initCases {
		t.Run(probe.name, func(t *testing.T) {
			manifest := providerManifest(t)
			if _, err := os.Stat(manifest); err != nil {
				t.Skip("no provider manifest in this checkout")
			}
			pythonRoot := filepath.Join(t.TempDir(), "project")
			goRoot := filepath.Join(t.TempDir(), "project")
			for _, root := range []string{pythonRoot, goRoot} {
				if err := os.MkdirAll(root, 0o755); err != nil {
					t.Fatal(err)
				}
				if probe.prepare != nil {
					probe.prepare(t, root)
				}
			}

			pythonCode, pythonOutput := runPythonKernel(repositoryRoot(t),
				append([]string{"--provider", manifest, "init", "--root", pythonRoot},
					probe.args...)...)
			var stdout, stderr bytes.Buffer
			goCode := Run(append([]string{"--provider", manifest, "init", "--root", goRoot},
				probe.args...), &stdout, &stderr)

			if pythonCode != probe.expectExit || goCode != probe.expectExit {
				t.Fatalf("expected exit %d -- python %d, go %d\npython: %s\ngo: %s",
					probe.expectExit, pythonCode, goCode, pythonOutput, stdout.String()+stderr.String())
			}
			// The roots differ by name and both appear in the output, so the
			// path is normalised. Nothing else is.
			python := strings.ReplaceAll(pythonOutput, pythonRoot, "<root>")
			golang := strings.ReplaceAll(stdout.String()+stderr.String(), goRoot, "<root>")
			if python != golang {
				t.Errorf("output differs.\npython:\n%s\ngo:\n%s", python, golang)
			}

			compareTrees(t, pythonRoot, goRoot, probe.writesNothing)
		})
	}
}

// compareTrees asserts the two projects hold the same files with the same
// bytes, root paths normalised.
func compareTrees(t *testing.T, pythonRoot, goRoot string, mayBeEmpty bool) {
	t.Helper()
	pythonFiles, goFiles := walkFiles(t, pythonRoot), walkFiles(t, goRoot)

	names := map[string]bool{}
	for name := range pythonFiles {
		names[name] = true
	}
	for name := range goFiles {
		names[name] = true
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)

	// Self-vacuity: two empty trees would compare equal, so an empty result is
	// only accepted where the case says it is the point.
	if len(sorted) == 0 && !mayBeEmpty {
		t.Fatal("neither kernel wrote anything; this comparison proves nothing")
	}
	if len(sorted) > 0 && mayBeEmpty {
		t.Errorf("this case should have written nothing and wrote %v", sorted)
	}
	for _, name := range sorted {
		python, inPython := pythonFiles[name]
		golang, inGo := goFiles[name]
		switch {
		case !inGo:
			t.Errorf("only the Python kernel wrote %s", name)
		case !inPython:
			t.Errorf("only the Go kernel wrote %s", name)
		// Both roots are normalised out of both files, not just each file's
		// own: a project copied from one root to the other carries the
		// original path inside it, and repair's fixtures are built that way.
		case normaliseRoots(python, pythonRoot, goRoot) !=
			normaliseRoots(golang, pythonRoot, goRoot):
			t.Errorf("%s differs.\npython:\n%s\ngo:\n%s", name, python, golang)
		}
	}
}

func normaliseRoots(content string, roots ...string) string {
	for _, root := range roots {
		content = strings.ReplaceAll(content, root, "<root>")
	}
	return content
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

func writeText(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func initialiseWith(t *testing.T, root string, args ...string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := Run(append([]string{"--provider", providerManifest(t), "init", "--root", root},
		args...), &stdout, &stderr); code != 0 {
		t.Fatalf("seeding an overlay: %s", stderr.String())
	}
}

// The invariants, stated without reference to the Python kernel.

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
