//go:build !windows

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func commandProcessGroupID(cmd *exec.Cmd) int {
	if cmd == nil || cmd.Process == nil {
		return 0
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		return cmd.Process.Pid
	}
	return pgid
}

func terminateProcessGroup(pgid int, force bool) error {
	if pgid <= 0 {
		return fmt.Errorf("invalid pgid: %d", pgid)
	}
	signal := syscall.SIGTERM
	if force {
		signal = syscall.SIGKILL
	}
	err := syscall.Kill(-pgid, signal)
	if err == syscall.ESRCH {
		return os.ErrProcessDone
	}
	return err
}
