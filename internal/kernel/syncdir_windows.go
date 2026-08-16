//go:build windows

package kernel

// syncDirectory does nothing here.
//
// Windows has no directory handle to flush -- opening a directory for that
// purpose is not supported, and MoveFileEx's replace is durable by a different
// mechanism. Returning nil is honest about there being nothing to do rather
// than reporting a failure for an operation the platform does not have.
func syncDirectory(string) error { return nil }
