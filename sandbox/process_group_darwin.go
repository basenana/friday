//go:build darwin

package sandbox

import (
	"os/exec"
	"syscall"
)

// Setsid isolates the wrapper and all descendants in a dedicated session.
// macOS has no reliable parent-death primitive equivalent to bwrap's
// --die-with-parent; descendants nevertheless inherit the Seatbelt profile.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
