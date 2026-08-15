//go:build !windows

package orchestration

import (
	"os"
	"syscall"
)

// fileIdentity is what a file *is*, as distinct from where it sits.
//
// A path can be made to point somewhere else between two operations; a
// device/inode pair cannot be forged by creating a different file at the same
// name. Comparing it is how a replacement is detected.
type fileIdentity struct {
	device uint64
	inode  uint64
	known  bool
}

func identityOf(info os.FileInfo) fileIdentity {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileIdentity{}
	}
	// The conversions are redundant on linux/amd64, where both fields are
	// already uint64, and required elsewhere -- Dev is int32 on darwin and
	// int64 on several BSDs. Kept so this file compiles on every Unix rather
	// than only the one the linter runs on.
	return fileIdentity{
		device: uint64(stat.Dev), //nolint:unconvert // Dev is not uint64 on every Unix
		inode:  uint64(stat.Ino), //nolint:unconvert // Ino likewise
		known:  true,
	}
}

// sameFile reports whether two identities describe the same file.
//
// Unknown identities compare false: a platform that cannot tell two files
// apart must not be told they are the same one.
func sameFile(a, b fileIdentity) bool {
	return a.known && b.known && a.device == b.device && a.inode == b.inode
}
