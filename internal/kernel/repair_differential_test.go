package kernel

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// `repair`, compared with the Python kernel on two copies of one initialised
// project.
//
// Both the report and the resulting tree are compared, because the two halves
// fail differently: a plan that names the right actions and then writes the
// wrong bytes passes a report-only check, and a plan that writes correctly
// while reporting nothing leaves an operator with no idea what happened.
//
// The exemption is the same one the other differentials carry, and no wider:
// where the failure comes from the platform rather than the kernel -- a JSON
// parser's message, an OS error's wording -- the two languages word it
// differently. Those cases are asserted on exit code and on which path is
// named, not on the sentence.

type repairCase struct {
	name string
	// break_ damages the project before repair runs. Applied to both copies.
	breakProject func(t *testing.T, root string)
	apply        bool
	// expectExit is asserted against both kernels.
	expectExit int
	// expectAction, when set, must appear in the report -- so a case cannot
	// pass by both kernels finding nothing to do.
	expectAction string
	// platformWorded marks the cases whose message belongs to the platform.
	platformWorded bool
}

func removeFile(parts ...string) func(*testing.T, string) {
	return func(t *testing.T, root string) {
		t.Helper()
		if err := os.Remove(filepath.Join(append([]string{root}, parts...)...)); err != nil {
			t.Fatal(err)
		}
	}
}

var repairCases = []repairCase{
	{name: "an intact project needs nothing"},
	{name: "an intact project, applied", apply: true},

	{
		name: "a missing overlay document", breakProject: removeFile(Overlay, "commands.json"),
		expectAction: "recreate_missing_baseline",
	},
	{
		name: "a missing overlay document, applied", breakProject: removeFile(Overlay, "commands.json"),
		apply: true, expectAction: "recreate_missing_baseline",
	},
	{
		name:         "a missing Claude wrapper",
		breakProject: removeFile(".claude", "agents", "api-contract-engineer.md"),
		expectAction: "recreate_missing_wrapper",
	},
	{
		name:         "a missing Claude wrapper, applied",
		breakProject: removeFile(".claude", "agents", "api-contract-engineer.md"),
		apply:        true, expectAction: "recreate_missing_wrapper",
	},
	{
		name:         "a missing Codex wrapper, applied",
		breakProject: removeFile(".codex", "agents", "cloud-architect.toml"),
		apply:        true, expectAction: "recreate_missing_wrapper",
	},
	{
		name: "a missing version lock, applied", breakProject: removeFile(Overlay, "version.lock"),
		apply: true, expectAction: "recreate_missing_lock",
	},
	{
		name: "a stale AGENTS.md block, applied",
		breakProject: func(t *testing.T, root string) {
			writeText(t, filepath.Join(root, "AGENTS.md"),
				"# House rules\n\n"+managedBlockStart+"\nstale\n"+managedBlockEnd+"\n\nafter\n")
		},
		apply: true, expectAction: "refresh_managed_block",
	},
	{
		name:         "an AGENTS.md missing entirely",
		breakProject: removeFile("AGENTS.md"),
		expectAction: "create_managed_block",
	},

	// Blockers. Each one is a case where the honest answer is that a human has
	// to look, and repair stops rather than doing the part it could.
	{
		name: "an ambiguous AGENTS.md block",
		breakProject: func(t *testing.T, root string) {
			writeText(t, filepath.Join(root, "AGENTS.md"), managedBlockStart+"\nhalf a block\n")
		},
		expectExit: 1,
	},
	{
		name: "no project identity at all", breakProject: removeFile(Overlay, "project.json"),
		expectExit: 1,
	},
	{
		// The provider profile has moved under a project that already made
		// decisions against the old one. Repair could regenerate the routing
		// to match, and that would silently re-route work.
		name: "the provider profile has changed",
		breakProject: func(t *testing.T, root string) {
			mutateJSON(t, filepath.Join(root, Overlay, "project.json"),
				func(document map[string]any) {
					document["profile_digest"] = "sha256:different"
				})
		},
		expectExit: 1,
	},
	{
		name: "a lock whose provenance moved",
		breakProject: func(t *testing.T, root string) {
			mutateJSON(t, filepath.Join(root, Overlay, "version.lock"),
				func(document map[string]any) {
					document["profile_digest"] = "sha256:moved"
				})
		},
		expectExit: 1,
	},
	{
		name: "a project.json that is not JSON",
		breakProject: func(t *testing.T, root string) {
			writeText(t, filepath.Join(root, Overlay, "project.json"), "not json at all")
		},
		expectExit: 1, platformWorded: true,
	},
}

func TestRepairAgreesWithThePythonKernel(t *testing.T) {
	for _, probe := range repairCases {
		t.Run(probe.name, func(t *testing.T) {
			pythonRoot, manifest := repairableProject(t)
			goRoot := filepath.Join(t.TempDir(), "project")
			if err := copyTree(pythonRoot, goRoot); err != nil {
				t.Fatal(err)
			}
			if probe.breakProject != nil {
				probe.breakProject(t, pythonRoot)
				probe.breakProject(t, goRoot)
			}

			args := []string{"repair"}
			if probe.apply {
				args = append(args, "--apply")
			}
			pythonCode, pythonOutput := runPythonKernel(repositoryRoot(t),
				append([]string{"--provider", manifest}, append(args, "--root", pythonRoot)...)...)
			var stdout, stderr bytes.Buffer
			goCode := Run(append([]string{"--provider", manifest},
				append(args, "--root", goRoot)...), &stdout, &stderr)

			if pythonCode != probe.expectExit || goCode != probe.expectExit {
				t.Fatalf("expected exit %d -- python %d, go %d\npython: %s\ngo: %s",
					probe.expectExit, pythonCode, goCode, pythonOutput, stdout.String()+stderr.String())
			}
			python := normaliseRoots(pythonOutput, pythonRoot, goRoot)
			golang := normaliseRoots(stdout.String()+stderr.String(), pythonRoot, goRoot)

			if probe.platformWorded {
				// The sentence belongs to whichever JSON parser produced it.
				// What must match is the verdict and which path it names.
				comparePaths(t, python, golang)
			} else if python != golang {
				t.Errorf("report differs.\npython:\n%s\ngo:\n%s", python, golang)
			}
			if probe.expectAction != "" && !strings.Contains(golang, probe.expectAction) {
				t.Errorf("no action mentioned %q; the case checks something else: %s",
					probe.expectAction, golang)
			}

			compareTrees(t, pythonRoot, goRoot, false)
		})
	}
}

// comparePaths compares two reports by the paths they name, not the wording.
func comparePaths(t *testing.T, python, golang string) {
	t.Helper()
	pythonPaths, goPaths := reportedPaths(t, python), reportedPaths(t, golang)
	if len(pythonPaths) == 0 {
		t.Error("the Python report named no path; this comparison proves nothing")
	}
	if strings.Join(pythonPaths, ",") != strings.Join(goPaths, ",") {
		t.Errorf("the reports name different paths: python %v, go %v", pythonPaths, goPaths)
	}
}

func reportedPaths(t *testing.T, report string) []string {
	t.Helper()
	var document struct {
		Status   string              `json:"status"`
		Actions  []map[string]string `json:"actions"`
		Blockers []map[string]string `json:"blockers"`
	}
	if err := json.Unmarshal([]byte(report), &document); err != nil {
		t.Fatalf("the report is not JSON: %s", report)
	}
	paths := []string{document.Status}
	for _, group := range [][]map[string]string{document.Actions, document.Blockers} {
		for _, item := range group {
			paths = append(paths, item["path"])
		}
	}
	return paths
}

// The invariants, stated without reference to the Python kernel.

func TestRepairNeverReplacesADecision(t *testing.T) {
	// The rule the whole command is built around: existing artifacts are
	// decisions, not cache. This edits every managed file, then repairs, and
	// asserts nothing it edited moved.
	root, manifest := repairableProject(t)
	mutateJSON(t, filepath.Join(root, Overlay, "authorities.json"),
		func(document map[string]any) {
			authority, _ := document["product_owner"].(map[string]any)
			authority["status"] = "assigned"
			authority["assignee"] = "github.com/somebody"
		})
	writeText(t, filepath.Join(root, ".claude", "agents", "api-contract-engineer.md"),
		"tailored by this project")
	// One genuinely missing file, so the repair has something to do and the
	// test is not asserting that a no-op changed nothing.
	if err := os.Remove(filepath.Join(root, Overlay, "commands.json")); err != nil {
		t.Fatal(err)
	}
	before := walkFiles(t, root)

	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	report, code, err := registry.Repair(RepairRequest{Root: root, Runner: "both", Apply: true})
	if err != nil || code != 0 {
		t.Fatalf("repair failed: code %d, %v, %s", code, err, RenderIndented(report))
	}
	if report.values["status"] != "repaired" {
		t.Fatalf("expected a repair to have happened, got %v", report.values["status"])
	}

	after := walkFiles(t, root)
	for name, content := range before {
		if after[name] != content {
			t.Errorf("%s was rewritten by repair", name)
		}
	}
	if _, recreated := after[filepath.Join(Overlay, "commands.json")]; !recreated {
		t.Error("the missing file was not recreated")
	}
}

func TestOneBlockerStopsEverything(t *testing.T) {
	// A repair that fixed what it could while something else was unreadable
	// would leave a project half-reconciled and report success.
	root, manifest := repairableProject(t)
	if err := os.Remove(filepath.Join(root, Overlay, "commands.json")); err != nil {
		t.Fatal(err)
	}
	writeText(t, filepath.Join(root, "AGENTS.md"), managedBlockStart+"\nhalf a block\n")
	before := walkFiles(t, root)

	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	report, code, err := registry.Repair(RepairRequest{Root: root, Runner: "both", Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if code == 0 || report.values["status"] != "blocked" {
		t.Fatalf("a blocked project reported %v with code %d", report.values["status"], code)
	}
	if report.values["mutation"] != false {
		t.Error("a blocked repair reported a mutation")
	}
	after := walkFiles(t, root)
	if len(after) != len(before) {
		t.Errorf("a blocked repair wrote: %d files before, %d after", len(before), len(after))
	}
}

func TestRepairWithoutApplyWritesNothing(t *testing.T) {
	root, manifest := repairableProject(t)
	if err := os.Remove(filepath.Join(root, Overlay, "commands.json")); err != nil {
		t.Fatal(err)
	}
	before := walkFiles(t, root)

	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	report, code, err := registry.Repair(RepairRequest{Root: root, Runner: "both"})
	if err != nil || code != 0 {
		t.Fatalf("repair failed: %v", err)
	}
	if report.values["status"] != "repair-available" {
		t.Fatalf("expected repair-available, got %v", report.values["status"])
	}
	if len(listOf(report.values["actions"])) == 0 {
		t.Fatal("nothing was proposed; this test would prove nothing")
	}
	if after := walkFiles(t, root); len(after) != len(before) {
		t.Error("a plan-only repair wrote to the project")
	}
}

func TestRepairRefusesToFollowASymlinkedManagedFile(t *testing.T) {
	// The reason this command has its own filesystem. A managed file replaced
	// by a link is refused outright -- reading it would report on a file
	// somewhere else, and writing it would write there.
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	root, manifest := repairableProject(t)
	outside := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(outside, []byte("untouched"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, Overlay, "commands.json")
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, target); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}

	registry := NewRegistry()
	if err := registry.LoadProvider(manifest); err != nil {
		t.Fatal(err)
	}
	report, code, err := registry.Repair(RepairRequest{Root: root, Runner: "both", Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if code == 0 {
		t.Errorf("a symlinked managed file was accepted: %s", RenderIndented(report))
	}
	if content := readFile(t, outside); content != "untouched" {
		t.Errorf("the file outside the project was written: %q", content)
	}
}
