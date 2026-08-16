package kernel

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Repair: what this kernel owes, stated without reference to any other
// implementation.
//
// Split out of repair_differential_test.go, which compares this kernel against the
// Python one and goes when it does. These say what the behaviour *is*, and
// stay. Keeping both in one file meant a whole-file deletion would have taken
// the second half with the first -- measured, that cost twenty points of
// coverage nobody intended to give up.

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
