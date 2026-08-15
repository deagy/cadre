//go:build !windows

package orchestration

import (
	"os/exec"
	"syscall"
)

// A dispatched child gets its own process group so a timeout can kill the
// whole tree, not just the process this package started.
//
// A coding CLI spawns its own children -- a language server, a test run, a
// package manager. Signalling only the parent leaves those orphaned and
// running, holding the sandbox open past the deadline that was supposed to
// close it, which is the failure mode a timeout exists to prevent.

func setProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup signals the whole group. The negative pid is the group.
func killProcessGroup(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
