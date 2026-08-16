//go:build !windows

package kernel

import "syscall"

// noFollowFlag makes open() fail when the final path component is a symlink,
// closing the check-then-open gap at the kernel level rather than in this
// process.
const noFollowFlag = syscall.O_NOFOLLOW

// NoFollowSupported reports whether the platform can refuse a symlink at open
// time. `repair` refuses to run where it cannot, rather than falling back to
// the ordinary path I/O it exists to avoid.
const NoFollowSupported = true
