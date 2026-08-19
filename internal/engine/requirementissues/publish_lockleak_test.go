//go:build !windows

package requirementissues

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
)

// A lock that cannot be written must not stay held.
//
// AcquireLock creates the lock with O_EXCL and only then writes the holder
// record. It used to return a write failure while leaving the file behind --
// and worse, returned the path alongside the error, which its only caller
// discards when the error is non-nil. Nobody released it, so a transient disk
// error escalated into a publish that stayed blocked until a human passed
// --break-lock.
//
// Injected with RLIMIT_FSIZE=0 rather than mocked: it leaves open(2) alone and
// makes every write fail, which is the exact shape of the bug. The limit and
// the SIGXFSZ disposition are process-wide, hence the child.
//
// The kernel carried the identical defect in its own AcquireLockFile; see
// internal/kernel/forgeledger_lockleak_test.go.
func TestAcquireLockReleasesTheLockWhenTheWriteFails(t *testing.T) {
	if root := os.Getenv("CADRE_PUBLISH_LOCK_FAILURE_ROOT"); root != "" {
		refuseToWriteThenExit(root)
	}

	root := t.TempDir()
	child := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.v")
	child.Env = append(os.Environ(), "CADRE_PUBLISH_LOCK_FAILURE_ROOT="+root)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("child process: %v\n%s", err, output)
	}

	// Asserted in the parent, against the real filesystem, once the child that
	// took the lock is gone.
	path := lockPath(root, lockLeakTaskID)
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Errorf("the lock file outlived a failed acquire (lstat: %v); "+
			"every later publish is blocked until a human passes --break-lock", err)
	}
}

const lockLeakTaskID = "task-lock-leak"

// refuseToWriteThenExit runs in the child: it makes writes fail, calls the real
// AcquireLock, and exits non-zero if that reported success.
func refuseToWriteThenExit(root string) {
	// Exceeding RLIMIT_FSIZE raises SIGXFSZ, whose default disposition kills
	// the process before Write can return EFBIG.
	signal.Ignore(syscall.SIGXFSZ)
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 0, Max: 0}); err != nil {
		fmt.Fprintf(os.Stderr, "setrlimit: %v\n", err)
		os.Exit(2)
	}
	if _, err := AcquireLock(root, lockLeakTaskID, false); err == nil {
		fmt.Fprintln(os.Stderr, "AcquireLock reported success while no byte could be written")
		os.Exit(3)
	}
	os.Exit(0)
}
