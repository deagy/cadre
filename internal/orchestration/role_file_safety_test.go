package orchestration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A role file is the dispatched agent's authority: its developer_instructions
// become the child's system prompt and its sandbox_mode helps decide what the
// child may touch. Reading the wrong bytes here is not a file-handling bug,
// it is a privilege decision made from someone else's text.
//
// These cover the refusals the Python original made through an O_NOFOLLOW
// open plus an S_ISREG check on the descriptor, which the Go port replaced
// with Lstat-for-size followed by os.ReadFile.

func writeRole(t *testing.T, path, instructions string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "model = \"claude-sonnet-5\"\ndeveloper_instructions = \"" + instructions + "\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestASymlinkedRoleFileIsRefused(t *testing.T) {
	// The attack: plant a symlink where a role file is looked up, pointing
	// anywhere this process can read. Its developer_instructions become the
	// dispatched child's system prompt.
	if !NoFollowSupported {
		t.Skip("platform cannot refuse a symlink at open time")
	}
	outside := filepath.Join(t.TempDir(), "attacker.toml")
	writeRole(t, outside, "INSTRUCTIONS FROM OUTSIDE THE PROJECT")

	tier := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(tier, "code-reviewer.toml")); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}

	_, err := readRoleFileCapped(filepath.Join(tier, "code-reviewer.toml"), MaxRoleFileBytes)
	if err == nil {
		t.Fatal("a symlinked role file was read")
	}
}

func TestANonRegularRoleFileIsRefused(t *testing.T) {
	// A directory is the portable case: on a platform without O_NOFOLLOW the
	// regular-file check on the descriptor is the whole defence, so it is
	// asserted separately rather than folded into the symlink test.
	tier := t.TempDir()
	directory := filepath.Join(tier, "code-reviewer.toml")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := readRoleFileCapped(directory, MaxRoleFileBytes)
	if err == nil {
		t.Fatal("a directory was accepted as a role file")
	}
	// Specifically the explicit refusal, not an incidental EISDIR from the
	// read. Accepting either would let the check be deleted while this test
	// still passed -- which is exactly what happened when it was written
	// loosely the first time.
	if !strings.Contains(err.Error(), "non-regular") {
		t.Errorf("refused for the wrong reason -- the regular-file check did not fire: %v", err)
	}
}

func TestTheSizeCapAppliesToWhatIsActuallyRead(t *testing.T) {
	// The cap was taken from an Lstat, which for a symlink reports the length
	// of the link target string -- a few dozen bytes -- so an oversized file
	// behind a symlink passed a cap that was never measuring it.
	//
	// Here the cap is checked directly: at the limit is fine, past it is a
	// refusal rather than a truncation. A role whose instructions were
	// silently cut in half still dispatches, with authority nobody wrote.
	tier := t.TempDir()
	path := filepath.Join(tier, "big.toml")
	if err := os.WriteFile(path, make([]byte, MaxRoleFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readRoleFileCapped(path, MaxRoleFileBytes); err == nil {
		t.Error("a file past the cap was accepted")
	}

	exact := filepath.Join(tier, "exact.toml")
	if err := os.WriteFile(exact, make([]byte, MaxRoleFileBytes), 0o644); err != nil {
		t.Fatal(err)
	}
	content, err := readRoleFileCapped(exact, MaxRoleFileBytes)
	if err != nil {
		t.Errorf("a file exactly at the cap was refused: %v", err)
	}
	if len(content) != MaxRoleFileBytes {
		t.Errorf("read %d bytes, want the whole file", len(content))
	}
}

func TestASymlinkAtAHigherTierDoesNotFallThroughToALowerOne(t *testing.T) {
	// Falling through would silently serve a different role than the one the
	// tampered file names, and hide the tampering. Refusing says so.
	if !NoFollowSupported {
		t.Skip("platform cannot refuse a symlink at open time")
	}
	project, global, plugin := t.TempDir(), t.TempDir(), t.TempDir()

	// A valid role exists at the global tier...
	writeRole(t, filepath.Join(global, "code-reviewer.toml"), "LEGITIMATE GLOBAL ROLE")
	// ...and a symlink shadows it at the project tier.
	outside := filepath.Join(t.TempDir(), "attacker.toml")
	writeRole(t, outside, "ATTACKER INSTRUCTIONS")
	if err := os.Symlink(outside, filepath.Join(project, "code-reviewer.toml")); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}

	role, err := ResolveRoleFileCodex("code-reviewer", project, global, plugin, ModePlanningOnly)
	if err == nil {
		t.Fatalf("resolution succeeded, returning the %q tier role", role.Tier)
	}
	if strings.Contains(err.Error(), "no .toml file found") {
		t.Error("the symlink was skipped rather than refused")
	}
}

func TestContainmentComparesPathComponentsNotStringPrefixes(t *testing.T) {
	// "/srv/project" is a string prefix of "/srv/project-attacker", so the
	// prefix form called a sibling directory contained.
	root := t.TempDir()
	inside := filepath.Join(root, "project")
	sibling := root + string(filepath.Separator) + "project-attacker"

	if err := ensureContained(filepath.Join(inside, "role.toml"), inside); err != nil {
		t.Errorf("a genuinely contained path was refused: %v", err)
	}
	if err := ensureContained(filepath.Join(sibling, "role.toml"), inside); err == nil {
		t.Error("a sibling directory sharing a name prefix was called contained")
	}
	if err := ensureContained(filepath.Join(root, "role.toml"), inside); err == nil {
		t.Error("a parent-directory path was called contained")
	}
}

func TestResolutionRecordsWhichRoleTextRan(t *testing.T) {
	// The digest was computed and discarded, and ID held the whole system
	// prompt. A path and a tier say where the file was, not what it said, and
	// a role file edited afterwards leaves no other trace.
	project, global, plugin := t.TempDir(), t.TempDir(), t.TempDir()
	writeRole(t, filepath.Join(global, "code-reviewer.toml"), "SPECIFIC ROLE TEXT")

	role, err := ResolveRoleFileCodex("code-reviewer", project, global, plugin, ModePlanningOnly)
	if err != nil {
		t.Fatal(err)
	}
	if role.ID != "code-reviewer" {
		t.Errorf("ID = %q, want the role id", role.ID)
	}
	if role.Tier != "global" {
		t.Errorf("Tier = %q, want which tier answered", role.Tier)
	}
	if len(role.InstructionsSHA256) != 64 {
		t.Errorf("InstructionsSHA256 = %q, want a sha256 hex digest", role.InstructionsSHA256)
	}

	// And it tracks the text: a different role file digests differently.
	other := t.TempDir()
	writeRole(t, filepath.Join(other, "code-reviewer.toml"), "DIFFERENT ROLE TEXT")
	changed, err := ResolveRoleFileCodex("code-reviewer", project, other, plugin, ModePlanningOnly)
	if err != nil {
		t.Fatal(err)
	}
	if changed.InstructionsSHA256 == role.InstructionsSHA256 {
		t.Error("different instructions produced the same digest")
	}
}

func TestAProjectTierRoleMustBeCommittedBeforeItCanWrite(t *testing.T) {
	// The project-tier role file is what an attacker with repository write
	// access controls most directly, so in scoped-repository-edit mode it
	// must be in git history before its instructions are trusted.
	//
	// The gate had no Go test at all; these are the three states that
	// distinguish it from no gate.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	global, plugin := t.TempDir(), t.TempDir()

	newRepo := func(t *testing.T) string {
		t.Helper()
		root := t.TempDir()
		for _, args := range [][]string{
			{"init", "-q"},
			{"config", "user.email", "probe@example.com"},
			{"config", "user.name", "probe"},
		} {
			if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v (%s)", args, err, out)
			}
		}
		return root
	}
	commitAll := func(t *testing.T, root string) {
		t.Helper()
		for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "role"}} {
			if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v (%s)", args, err, out)
			}
		}
	}

	t.Run("committed and clean is trusted", func(t *testing.T) {
		root := newRepo(t)
		writeRole(t, filepath.Join(root, "code-reviewer.toml"), "COMMITTED")
		commitAll(t, root)
		if _, err := ResolveRoleFileCodex("code-reviewer", root, global, plugin, ModeRepositoryEdit); err != nil {
			t.Errorf("a committed, clean role file was refused: %v", err)
		}
	})

	t.Run("untracked is refused", func(t *testing.T) {
		root := newRepo(t)
		writeRole(t, filepath.Join(root, "code-reviewer.toml"), "UNTRACKED")
		if _, err := ResolveRoleFileCodex("code-reviewer", root, global, plugin, ModeRepositoryEdit); err == nil {
			t.Error("an untracked role file was trusted in write mode")
		}
	})

	t.Run("tracked but modified is refused", func(t *testing.T) {
		// The case a tracked-only check would miss: commit a harmless role,
		// then edit it without committing.
		root := newRepo(t)
		path := filepath.Join(root, "code-reviewer.toml")
		writeRole(t, path, "HARMLESS")
		commitAll(t, root)
		writeRole(t, path, "EDITED AFTER COMMIT")
		if _, err := ResolveRoleFileCodex("code-reviewer", root, global, plugin, ModeRepositoryEdit); err == nil {
			t.Error("a tracked but uncommitted-edit role file was trusted in write mode")
		}
	})

	t.Run("planning-review-only does not apply the gate", func(t *testing.T) {
		// Read-only dispatch cannot use the authority the gate protects, and
		// applying it there would make an uncommitted role undiagnosable.
		root := newRepo(t)
		writeRole(t, filepath.Join(root, "code-reviewer.toml"), "UNTRACKED")
		if _, err := ResolveRoleFileCodex("code-reviewer", root, global, plugin, ModePlanningOnly); err != nil {
			t.Errorf("the gate was applied outside write mode: %v", err)
		}
	})
}
