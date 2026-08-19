//go:build !windows

package orchestration

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A wrapper that could not be written must not poison the next run.
//
// writeOwnedWrapper creates the destination with O_EXCL and then writes it. It
// used to return a write failure while leaving the empty file behind, and the
// consequence was not a lost byte: the next run takes the "already exists"
// path, reads empty content, finds no provenance marker, and refuses the file
// as an unowned wrapper -- permanently, and telling the operator the file
// belongs to someone else when this function created and abandoned it.
//
// So the assertion is about the *second* call, not the first: it is the
// recoverability that regressed, and a test that only checked for the leftover
// file would not say why that matters.
//
// RLIMIT_FSIZE=0 injects the failure -- it leaves open(2) alone and makes every
// write fail, which is the shape of the bug -- in a child process, because the
// limit and the SIGXFSZ disposition are process-wide.
func TestWriteOwnedWrapperLeavesNothingBehindWhenTheWriteFails(t *testing.T) {
	if destination := os.Getenv("CADRE_WRAPPER_WRITE_FAILURE_PATH"); destination != "" {
		refuseToWriteThenExit(destination)
	}

	destination := filepath.Join(t.TempDir(), "cadre-wrapper.toml")
	child := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.v")
	child.Env = append(os.Environ(), "CADRE_WRAPPER_WRITE_FAILURE_PATH="+destination)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("child process: %v\n%s", err, output)
	}

	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		t.Errorf("the wrapper outlived a failed write (lstat: %v)", err)
	}

	// The point of the fix: a run that can write must still succeed here.
	content := []byte("# " + ProvenanceMarker + "\nname = \"x\"\n")
	status, err := writeOwnedWrapper(destination, content)
	if err != nil {
		if strings.Contains(err.Error(), "unowned") {
			t.Fatalf("a failed write poisoned the destination: the next run refuses "+
				"its own abandoned file as unowned: %v", err)
		}
		t.Fatalf("writeOwnedWrapper after a failed attempt: %v", err)
	}
	if status != "installed" {
		t.Errorf("status = %q, want \"installed\"", status)
	}
}

// refuseToWriteThenExit runs in the child: it makes writes fail, calls the real
// writeOwnedWrapper, and exits non-zero if that reported success.
func refuseToWriteThenExit(destination string) {
	// Exceeding RLIMIT_FSIZE raises SIGXFSZ, whose default disposition kills
	// the process before Write can return EFBIG.
	signal.Ignore(syscall.SIGXFSZ)
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 0, Max: 0}); err != nil {
		fmt.Fprintf(os.Stderr, "setrlimit: %v\n", err)
		os.Exit(2)
	}
	if _, err := writeOwnedWrapper(destination, []byte("# "+ProvenanceMarker+"\nname = \"x\"\n")); err == nil {
		fmt.Fprintln(os.Stderr, "writeOwnedWrapper reported success while no byte could be written")
		os.Exit(3)
	}
	os.Exit(0)
}
