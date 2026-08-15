//go:build windows

package orchestration

// noFollowFlag is zero on Windows: syscall.O_NOFOLLOW does not exist there.
//
// The open therefore follows a symlink at the final component, and
// containment rests on the resolve-then-open sequence alone -- a gap a
// symlink appearing in between would defeat. Callers re-check
// regular-file-ness on the open handle, which still refuses a FIFO, a device
// node, or a directory that appeared at the same path.
//
// Stated rather than hidden: this is the same trade internal/orchestration/
// sync_codex_agents.go documents, and it is narrower than the Python
// original's guarantee on every platform that has the flag.
const noFollowFlag = 0

// NoFollowSupported reports whether the platform can refuse a symlink at open
// time.
const NoFollowSupported = false
