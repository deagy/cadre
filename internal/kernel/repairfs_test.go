package kernel

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The descriptor-confined filesystem repair writes through.
//
// These are the tests that justify the type existing at all. Ordinary path
// I/O would pass every functional test in this file; what it would fail is the
// symlink cases, and those are the whole point -- repair writes into a project
// it did not create, on a filesystem somebody else may be touching.

func repairFilesystem(t *testing.T) (*RepairFilesystem, string) {
	t.Helper()
	root := t.TempDir()
	filesystem, err := OpenRepairFilesystem(root)
	if err != nil {
		t.Fatalf("opening a plain directory: %v", err)
	}
	t.Cleanup(func() { _ = filesystem.Close() })
	return filesystem, root
}

func TestARepairPathComponentCannotTraverse(t *testing.T) {
	filesystem, _ := repairFilesystem(t)
	for _, hostile := range [][]string{
		{".."},
		{Overlay, "..", "..", "etc", "passwd"},
		{"", "project.json"},
		{".", "project.json"},
		{"a/b"},
		{`a\b`},
	} {
		if _, err := filesystem.FileState(hostile...); err == nil {
			t.Errorf("%v was accepted", hostile)
		}
	}
}

func TestASymlinkIsRefusedRatherThanFollowed(t *testing.T) {
	// The case ordinary path I/O gets wrong. os.Root alone would follow a link
	// that stays inside the root -- which still means repair writes somewhere
	// other than where it looked.
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	filesystem, root := repairFilesystem(t)

	target := filepath.Join(root, "real.json")
	if err := os.WriteFile(target, []byte(`{"real": true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.json", filepath.Join(root, "link.json")); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
	if _, err := filesystem.FileState("link.json"); err == nil {
		t.Error("a symlink to a file inside the root was accepted")
	}
	if _, err := filesystem.ReadText("link.json"); err == nil {
		t.Error("a symlink was read through")
	}

	// A symlinked *directory* on the way is refused too, which is the harder
	// half: the file at the end of it is perfectly ordinary.
	if err := os.MkdirAll(filepath.Join(root, "elsewhere"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "elsewhere", "file.json"),
		[]byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("elsewhere", filepath.Join(root, "linkdir")); err != nil {
		t.Fatal(err)
	}
	if _, err := filesystem.FileState("linkdir", "file.json"); err == nil {
		t.Error("a path through a symlinked directory was accepted")
	}
}

func TestARootThatIsItselfASymlinkIsRefused(t *testing.T) {
	// Not resolved before opening, deliberately: resolving would silently
	// accept a final-component symlink an operator handed in as --root.
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	parent := t.TempDir()
	real := filepath.Join(parent, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
	if filesystem, err := OpenRepairFilesystem(link); err == nil {
		_ = filesystem.Close()
		t.Error("a symlinked project root was opened")
	}
}

func TestWritingCreatesAndRefusesToClobber(t *testing.T) {
	filesystem, root := repairFilesystem(t)

	written, err := filesystem.WriteText([]string{Overlay, "project.json"}, `{"a": 1}`, false)
	if err != nil {
		t.Fatalf("creating: %v", err)
	}
	if !written {
		t.Error("creating a missing file reported no write")
	}
	if content := readFile(t, filepath.Join(root, Overlay, "project.json")); content != `{"a": 1}` {
		t.Errorf("wrote %q", content)
	}

	// The decision case: something is already there, and repair is not allowed
	// to replace it. Reported as "not written" rather than as an error --
	// finding a decision already made is a normal outcome.
	written, err = filesystem.WriteText([]string{Overlay, "project.json"}, `{"b": 2}`, false)
	if err != nil {
		t.Fatalf("declining to clobber returned an error: %v", err)
	}
	if written {
		t.Error("an existing file was overwritten by a create")
	}
	if content := readFile(t, filepath.Join(root, Overlay, "project.json")); content != `{"a": 1}` {
		t.Errorf("the existing file changed to %q", content)
	}

	// And an explicit overwrite does replace it, or the check above passes by
	// never writing anything.
	if _, err := filesystem.WriteText([]string{Overlay, "project.json"}, `{"b": 2}`, true); err != nil {
		t.Fatal(err)
	}
	if content := readFile(t, filepath.Join(root, Overlay, "project.json")); content != `{"b": 2}` {
		t.Errorf("the overwrite left %q", content)
	}
}

func TestWritingLeavesNoTemporaryFileBehind(t *testing.T) {
	// The install goes through a temporary file in the pinned directory. One
	// left behind would be picked up by anything walking the overlay, and a
	// repair that litters is a repair somebody stops trusting.
	filesystem, root := repairFilesystem(t)
	for _, name := range []string{"one.json", "two.json"} {
		if _, err := filesystem.WriteText([]string{Overlay, name}, "{}", false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := filesystem.WriteText([]string{Overlay, "one.json"}, `{"x": 1}`, true); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(filepath.Join(root, Overlay))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".agentic-sdlc-repair-") {
			t.Errorf("a temporary file survived: %s", entry.Name())
		}
	}
	if len(entries) != 2 {
		t.Errorf("expected two files, found %d", len(entries))
	}
}

func TestWritingRefusesASymlinkAtTheTargetName(t *testing.T) {
	// A symlink sitting at the name repair is about to write is refused, in
	// both modes, and the file it points at is left alone. Following it is how
	// a repair inside a project writes outside one.
	//
	// The narrower case the design also covers -- a link raced in *after* this
	// check -- is handled by rename replacing the name rather than following
	// it. That one cannot be observed from a test without winning a race
	// against the implementation, so it is documented at the call site rather
	// than asserted here.
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	for _, overwrite := range []bool{false, true} {
		filesystem, root := repairFilesystem(t)
		outside := filepath.Join(t.TempDir(), "victim")
		if err := os.WriteFile(outside, []byte("untouched"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, Overlay), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, Overlay, "project.json")); err != nil {
			t.Skipf("cannot create symlinks here: %v", err)
		}

		written, err := filesystem.WriteText(
			[]string{Overlay, "project.json"}, `{"unsafe": true}`, overwrite)
		if written || err == nil {
			t.Errorf("overwrite=%v: a symlinked target name was written", overwrite)
		}
		if content := readFile(t, outside); content != "untouched" {
			t.Errorf("overwrite=%v: the file outside the project was written: %q",
				overwrite, content)
		}
	}
}

func TestCreatingLosesARaceRatherThanWinningIt(t *testing.T) {
	// A create must fail if something appeared at the name after planning.
	// That is what makes "only what is missing" true rather than aspirational.
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	filesystem, root := repairFilesystem(t)
	outside := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(outside, []byte("untouched"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, Overlay), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, Overlay, "routing.json")); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}

	written, err := filesystem.WriteText([]string{Overlay, "routing.json"}, `{"unsafe": true}`, false)
	if written {
		t.Error("a create reported success against an occupied name")
	}
	if err == nil {
		t.Error("a symlink at the target name was not reported")
	}
	if content := readFile(t, outside); content != "untouched" {
		t.Errorf("the file outside the project was written: %q", content)
	}
}

func TestReadingAndStatingAgreeAboutWhatExists(t *testing.T) {
	filesystem, root := repairFilesystem(t)

	state, err := filesystem.FileState(Overlay, "absent.json")
	if err != nil {
		t.Fatalf("stating a missing file in a missing directory: %v", err)
	}
	if state != "missing" {
		t.Errorf("a missing file reported %q", state)
	}
	if _, err := filesystem.ReadText(Overlay, "absent.json"); err == nil {
		t.Error("reading a missing file succeeded")
	}

	if err := os.MkdirAll(filepath.Join(root, Overlay), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, Overlay, "present.json"),
		[]byte(`{"present": true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err = filesystem.FileState(Overlay, "present.json")
	if err != nil {
		t.Fatal(err)
	}
	if state != "regular" {
		t.Errorf("an ordinary file reported %q", state)
	}
	content, err := filesystem.ReadText(Overlay, "present.json")
	if err != nil {
		t.Fatal(err)
	}
	if content != `{"present": true}` {
		t.Errorf("read %q", content)
	}

	// A directory where a file is expected is an error, not a state: repair
	// would otherwise try to install a file over it.
	if err := os.MkdirAll(filepath.Join(root, Overlay, "directory.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := filesystem.FileState(Overlay, "directory.json"); err == nil {
		t.Error("a directory was reported as a file state")
	}
}
