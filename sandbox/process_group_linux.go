//go:build linux

package sandbox

import (
	"os/exec"
	"syscall"
)

// Keep host-side cancellation in a dedicated process group. bubblewrap creates
// the sandbox-side session with --new-session.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
