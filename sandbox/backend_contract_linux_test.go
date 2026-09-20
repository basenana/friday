//go:build linux

package sandbox

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestBackendContractLinuxNamespacesAndCapabilities(t *testing.T) {
	workdir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem = FilesystemConfig{}
	executor := nativeBackendExecutor(t, cfg)

	result := runBackendContract(t, executor,
		"test \"$(grep -Ec '^Cap(Inh|Prm|Eff|Bnd|Amb):[[:space:]]*0000000000000000$' /proc/self/status)\" = 5",
		workdir, workdir)
	requireContractSuccess(t, result)
	result = runBackendContract(t, executor, "test ! -e /dev/full && test ! -e /dev/tty && test ! -e /dev/ptmx", workdir, workdir)
	requireContractSuccess(t, result)

	hostIPC, err := os.Readlink("/proc/self/ns/ipc")
	if err != nil {
		t.Fatalf("read host IPC namespace: %v", err)
	}
	result = runBackendContract(t, executor, "readlink /proc/self/ns/ipc", workdir, workdir)
	requireContractSuccess(t, result)
	if strings.TrimSpace(result.Stdout) == hostIPC {
		t.Fatalf("sandbox shares host IPC namespace %q", hostIPC)
	}

	helper := exec.Command("sleep", "10")
	if err := helper.Start(); err != nil {
		t.Fatalf("start host helper: %v", err)
	}
	t.Cleanup(func() {
		_ = helper.Process.Kill()
		_ = helper.Wait()
	})
	hostPIDPath := "/proc/" + strconv.Itoa(helper.Process.Pid)
	if os.Getenv("FRIDAY_SANDBOX_PROC_BIND") == "" {
		result = runBackendContract(t, executor, "test ! -e "+hostPIDPath, workdir, workdir)
	} else {
		result = runBackendContract(t, executor, "test -e "+hostPIDPath, workdir, workdir)
	}
	requireContractSuccess(t, result)

	result = runBackendContract(t, executor, contractHostPIDCommand(helper.Process.Pid), workdir, workdir)
	requireContractFailure(t, result)
	if err := helper.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("sandbox affected external helper: %v", err)
	}
}
