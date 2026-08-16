//go:build !windows

package kernel

import "os"

// syncDirectory flushes a directory entry so a rename survives a crash.
//
// The rename is atomic with respect to a reader, but not durable with respect
// to power loss: the new file's data is on disk after its own fsync, and the
// directory entry pointing at it may not be. Without this, a crash can leave
// the old ledger in place while the new one exists unreferenced -- and the
// next run publishes to the forge again on the strength of the stale one.
func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return err
	}
	return directory.Close()
}
