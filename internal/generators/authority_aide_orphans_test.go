package generators

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Removing the aides that are no longer generated.
//
// `cadre generate-authority-aides` writes one AGENT.md per aide declared in
// aides.yaml. Drop an aide from that file and its directory is left behind --
// and CheckAides reports the leftover as stale, so `generate` produced a tree
// its own `--check` rejects. The fix for a red CI job was a manual delete the
// command should have done.
//
// The deletion is deliberately narrow, because this is a generator that
// removes files. It touches exactly <authorityRoot>/*/AGENT.md paths the
// current aide set does not generate, and removes the parent directory only
// when that leaves it empty.

func aideTree(t *testing.T, present ...string) (root string, generated map[string]string) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "authority")
	generated = map[string]string{}
	for _, name := range present {
		directory := filepath.Join(root, name)
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "AGENT.md"),
			[]byte("# "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, generated
}

func TestAnAideNoLongerGeneratedIsRemoved(t *testing.T) {
	root, generated := aideTree(t, "kept-aide", "dropped-aide")
	// Only one of the two is still generated.
	generated[filepath.Join(root, "kept-aide", "AGENT.md")] = "# kept-aide\n"

	kept, err := RemoveOrphanedAides(root, generated)
	if err != nil {
		t.Fatalf("RemoveOrphanedAides: %v", err)
	}
	if len(kept) != 0 {
		t.Errorf("an empty orphan directory was kept: %v", kept)
	}
	if _, err := os.Stat(filepath.Join(root, "dropped-aide")); !os.IsNotExist(err) {
		t.Error("the orphaned aide directory is still there")
	}
	if _, err := os.Stat(filepath.Join(root, "kept-aide", "AGENT.md")); err != nil {
		t.Errorf("an aide that is still generated was removed: %v", err)
	}
}

func TestAnOrphanDirectoryHoldingOtherFilesKeepsThem(t *testing.T) {
	// The case that decides how much this may delete. A directory containing
	// something the generator did not write is not the generator's to remove:
	// the AGENT.md goes, the rest stays, and the caller is told.
	//
	// A recursive delete here would be one bad path away from removing work
	// nobody asked it to touch.
	root, generated := aideTree(t, "dropped-aide")
	note := filepath.Join(root, "dropped-aide", "NOTES.md")
	if err := os.WriteFile(note, []byte("a note somebody left\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	kept, err := RemoveOrphanedAides(root, generated)
	if err != nil {
		t.Fatalf("RemoveOrphanedAides: %v", err)
	}
	if len(kept) != 1 || !strings.HasSuffix(kept[0], "dropped-aide") {
		t.Errorf("the retained directory was not reported: %v", kept)
	}
	if _, err := os.Stat(filepath.Join(root, "dropped-aide", "AGENT.md")); !os.IsNotExist(err) {
		t.Error("the orphaned AGENT.md was not removed")
	}
	content, err := os.ReadFile(note)
	if err != nil {
		t.Fatalf("a file the generator did not write was destroyed: %v", err)
	}
	if string(content) != "a note somebody left\n" {
		t.Errorf("the retained file was altered: %q", content)
	}
}

func TestRemovalTouchesNothingOutsideItsOwnShape(t *testing.T) {
	// The scope of the delete, stated as what survives. Files directly under
	// the authority root -- aides.yaml and the template live there -- and any
	// file that is not named AGENT.md are outside what this generates and must
	// not be considered orphans.
	root, generated := aideTree(t, "kept-aide")
	generated[filepath.Join(root, "kept-aide", "AGENT.md")] = "# kept-aide\n"

	siblings := map[string]string{
		filepath.Join(root, "aides.yaml"):         "aides: []\n",
		filepath.Join(root, "_template.md.tmpl"):  "template\n",
		filepath.Join(root, "kept-aide", "EXTRA"): "not an AGENT.md\n",
	}
	for path, body := range siblings {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := RemoveOrphanedAides(root, generated); err != nil {
		t.Fatalf("RemoveOrphanedAides: %v", err)
	}
	for path, body := range siblings {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s was removed; it is not something this generates", path)
			continue
		}
		if string(content) != body {
			t.Errorf("%s was altered", path)
		}
	}
}

func TestOrphanDetectionAndRemovalAgreeWithCheck(t *testing.T) {
	// The bug this fixes, stated as the property that was broken: after a
	// write, `--check` must pass. CheckAides reported the orphan as stale
	// while the write path had no way to clear it, so the two halves of one
	// command disagreed about whether the tree was current.
	root, generated := aideTree(t, "kept-aide", "dropped-aide")
	generated[filepath.Join(root, "kept-aide", "AGENT.md")] = "# kept-aide\n"

	current, stale, err := CheckAides(root, generated)
	if err != nil {
		t.Fatal(err)
	}
	if current || len(stale) == 0 {
		t.Fatal("the orphan was not reported as stale, so this case starts from " +
			"the wrong state")
	}

	if _, err := RemoveOrphanedAides(root, generated); err != nil {
		t.Fatalf("RemoveOrphanedAides: %v", err)
	}
	if err := WriteAideFiles(generated); err != nil {
		t.Fatalf("WriteAideFiles: %v", err)
	}

	current, stale, err = CheckAides(root, generated)
	if err != nil {
		t.Fatal(err)
	}
	if !current {
		t.Errorf("after removing orphans and writing, check still reports stale: %v", stale)
	}
}

func TestAnUnreadableAuthorityRootIsAnErrorRatherThanNoOrphans(t *testing.T) {
	// CheckAides discards this error and reports no orphans, which reads as
	// "the tree is clean". The removal path says so instead: a root it cannot
	// read is a fact about the run, not a finding about the tree.
	_, err := OrphanedAideFiles(filepath.Join(t.TempDir(), "absent"), map[string]string{})
	if err == nil {
		t.Error("an authority root that does not exist reported no orphans")
	}
}
