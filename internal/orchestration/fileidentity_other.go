//go:build windows

package orchestration

import "os"

// Windows exposes no stable device/inode pair through os.FileInfo, so file
// identity cannot be compared here.
//
// Stated rather than hidden: the replacement check that guards the
// final-handoff result file is therefore unavailable on Windows, and the
// retained file handle is the whole defence. That handle still refers to the
// original file no matter what appears at the path afterwards, so a
// replacement is not *read* -- it simply cannot be reported as one.
type fileIdentity struct{ known bool }

func identityOf(os.FileInfo) fileIdentity { return fileIdentity{} }

func sameFile(fileIdentity, fileIdentity) bool { return false }
