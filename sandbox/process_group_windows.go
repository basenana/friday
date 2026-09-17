//go:build windows

package sandbox

import (
	"fmt"
	"os/exec"
	"strconv"
)

func configureProcessGroup(_ *exec.Cmd) {}

func commandProcessGroupID(cmd *exec.Cmd) int {
	if cmd == nil || cmd.Process == nil {
		return 0
	}
	return cmd.Process.Pid
}

func terminateProcessGroup(pid int, force bool) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid: %d", pid)
	}
	args := []string{"/PID", strconv.Itoa(pid), "/T"}
	if force {
		args = append(args, "/F")
	}
	return exec.Command("taskkill", args...).Run()
}
