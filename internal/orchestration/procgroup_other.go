//go:build windows

package orchestration

import "os/exec"

// Windows has no process groups in the POSIX sense, so a timeout reaches the
// child this package started and no further. Stated rather than hidden: a
// coding CLI's own grandchildren can outlive the deadline here, which is a
// narrower guarantee than every other platform gets.

func setProcessGroup(command *exec.Cmd) {}

func killProcessGroup(pid int) {}
