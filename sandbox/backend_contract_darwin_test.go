//go:build darwin

package sandbox

import (
	"os/exec"
	"strconv"
	"syscall"
	"testing"
)

func TestBackendContractDarwinProcessBoundary(t *testing.T) {
	workdir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Sandbox.Filesystem = FilesystemConfig{}
	executor := nativeBackendExecutor(t, cfg)

	result := runBackendContract(t, executor, "sleep 5 & child=$!; kill -0 $child; kill $child; wait $child; test $? -gt 0", workdir, workdir)
	requireContractSuccess(t, result)

	helper := exec.Command("sleep", "10")
	if err := helper.Start(); err != nil {
		t.Fatalf("start host helper: %v", err)
	}
	t.Cleanup(func() {
		_ = helper.Process.Kill()
		_ = helper.Wait()
	})
	pid := strconv.Itoa(helper.Process.Pid)
	requireContractFailure(t, runBackendContract(t, executor, "kill -0 "+pid, workdir, workdir))
	requireContractFailure(t, runBackendContract(t, executor, "/bin/ps -p "+pid+" -o pid=", workdir, workdir))
	if err := helper.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("sandbox affected external helper: %v", err)
	}
}
