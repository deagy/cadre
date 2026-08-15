//go:build !windows

package orchestration

import "syscall"

// noFollowFlag makes open() fail with ELOOP when the final path component is
// a symlink, closing the check-then-open gap at the kernel level.
//
// Containment is decided by resolving a path and proving it lands inside the
// project. Between that check and the open, a symlink appearing at the final
// component would redirect the operation anywhere this process can reach --
// and dispatch runs team members concurrently against one project root, so
// that interleaving is not theoretical.
const noFollowFlag = syscall.O_NOFOLLOW

// NoFollowSupported reports whether the platform can refuse a symlink at open
// time. Callers that must not silently weaken use it to refuse instead.
const NoFollowSupported = true
