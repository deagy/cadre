package kernel

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Durable writes and advisory locks for the forge sidecar ledgers.
//
// Every ledger this kernel keeps -- gate issues, gate status, reviewer nudges
// -- records what was published to a forge. That makes them the only record of
// something that happened outside this machine: if a ledger loses an entry,
// the next run creates a second issue for a gate that already has one, and
// nothing detects the duplicate because the ledger is what detection reads.
//
// So the write is durable rather than convenient: a temporary file on the same
// filesystem, fsynced, atomically renamed, and the containing directory fsynced
// afterwards so the rename itself survives a crash.
//
// The lock is advisory and is never broken automatically. A stale lock means
// somebody's publication was interrupted, and the safe response is to make a
// human look -- an automatic timeout would resume a half-finished publication
// with no idea what the interrupted run had already created.

// LedgerLockHeld reports a lock that another run holds.
type LedgerLockHeld struct {
	Path   string
	Holder string
}

func (e *LedgerLockHeld) Error() string {
	return fmt.Sprintf(
		"%s is already held -- pass --break-lock to override "+
			"(never auto-broken on timeout). Holder:\n%s",
		filepath.Base(e.Path), e.Holder)
}

// LedgerPath is where a task's sidecar ledger lives.
func LedgerPath(root, overlay, taskID, filename string) (string, error) {
	return ConfinedPath(root, overlay, "runs", taskID, filename)
}

// WriteLedgerFile replaces a ledger durably.
//
// Full-file rewrite rather than an append or an in-place edit: a ledger is
// small, and a torn append leaves a file that parses as valid JSON with an
// entry missing -- which is worse than one that fails to parse, because
// nothing notices.
func WriteLedgerFile(path string, ledger any, temporaryPrefix string) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	payload, err := canonicalLedgerBytes(ledger)
	if err != nil {
		return err
	}

	temporary, err := os.CreateTemp(directory, temporaryPrefix+"*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(temporaryName)
		}
	}()

	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		return err
	}
	// Data first, then the rename, then the directory. Skipping the last one
	// leaves a rename that can be lost while the file it points at survives.
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Chmod(temporaryName, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return err
	}
	cleanup = false
	return syncDirectory(directory)
}

// canonicalLedgerBytes renders a ledger the way the Python kernel writes one:
// sorted keys, two-space indent, trailing newline.
//
// Sorted rather than insertion-ordered, unlike every other document this
// kernel writes. A ledger is rewritten in full on each publication, and a
// stable key order is what makes its diff readable -- the entries change, the
// shape does not.
func canonicalLedgerBytes(ledger any) ([]byte, error) {
	sorted, err := sortedIndentedJSON(ledger)
	if err != nil {
		return nil, err
	}
	return append(sorted, '\n'), nil
}

// AcquireLockFile takes the advisory lock for a ledger.
//
// O_CREAT|O_EXCL: the existence of the file is the lock, and creating it is
// the only atomic operation available across every filesystem this may run on.
func AcquireLockFile(path string, breakLock bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if breakLock {
		// Only on an explicit --break-lock. Never on a timeout: a lock that
		// has been held a long time is a publication that was interrupted, and
		// the interrupted run may have created issues this one is about to
		// create again.
		if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
			if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|noFollowFlag, 0o600)
	if errors.Is(err, fs.ErrExist) {
		holder := "(unreadable)"
		if data, readErr := os.ReadFile(path); readErr == nil {
			holder = string(data)
		}
		return &LedgerLockHeld{Path: path, Holder: holder}
	}
	if err != nil {
		return err
	}
	// Who holds it, from where, and since when -- so the human the refusal
	// sends to the lock file can find the run that left it.
	payload, err := sortedIndentedJSON(map[string]any{
		"pid":        os.Getpid(),
		"host":       hostname(),
		"started_at": nowRFC3339(),
	})
	if err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

// ReleaseLockFile drops the lock, tolerating its absence.
//
// Absent is not an error: a run that failed after breaking a lock, or one
// whose lock was broken under it, should still exit cleanly rather than
// reporting a second failure on the way out.
func ReleaseLockFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return name
}
