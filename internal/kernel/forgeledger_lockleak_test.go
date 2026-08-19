//go:build !windows

package kernel

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
)

// A lock that cannot be written must not stay held.
//
// AcquireLockFile creates the lock with O_EXCL and only then writes the holder
// record. It used to return a write failure without removing the file it had
// just created -- and every caller registers its deferred ReleaseLockFile only
// after a successful acquire, so nobody released it. The abandoned lock then
// blocked every later run until a human passed --break-lock: a transient disk
// error escalated into a permanent, manual-recovery outage.
//
// The failure is injected rather than mocked. RLIMIT_FSIZE=0 leaves open(2)
// alone and makes every write fail, which is the exact shape of the bug; a
// fake filesystem would have proved something about the fake. It runs in a
// child process because the limit and the SIGXFSZ disposition are
// process-wide and would otherwise follow the rest of the suite.
func TestAcquireLockFileReleasesTheLockWhenTheWriteFails(t *testing.T) {
	if path := os.Getenv("CADRE_LOCK_WRITE_FAILURE_PATH"); path != "" {
		refuseToWriteThenExit(path)
	}

	path := filepath.Join(t.TempDir(), "nested", "forge.lock")
	child := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.v")
	child.Env = append(os.Environ(), "CADRE_LOCK_WRITE_FAILURE_PATH="+path)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("child process: %v\n%s", err, output)
	}

	// The assertion is deliberately in the parent, against the real
	// filesystem, after the child that took the lock is gone.
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Errorf("the lock file outlived a failed acquire (lstat: %v); "+
			"every later run is blocked until a human passes --break-lock", err)
	}
}

// refuseToWriteThenExit runs in the child: it makes writes fail, calls the
// real AcquireLockFile, and exits non-zero if that reported success.
func refuseToWriteThenExit(path string) {
	// Exceeding RLIMIT_FSIZE raises SIGXFSZ, whose default disposition kills
	// the process before Write can return EFBIG.
	signal.Ignore(syscall.SIGXFSZ)
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 0, Max: 0}); err != nil {
		fmt.Fprintf(os.Stderr, "setrlimit: %v\n", err)
		os.Exit(2)
	}
	if err := AcquireLockFile(path, false); err == nil {
		fmt.Fprintln(os.Stderr, "AcquireLockFile reported success while no byte could be written")
		os.Exit(3)
	}
	os.Exit(0)
}
