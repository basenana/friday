package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func newTestTaskManager() *TaskManager {
	cfg := DefaultConfig()
	cfg.Sandbox.Enabled = false
	cfg.Permissions.Allow = append(cfg.Permissions.Allow, "sleep", "echo", "sh", "true", "false", "cat", "pwd", "python3")
	exec := NewExecutor(cfg)
	return NewTaskManager(exec)
}

func waitForTask(t *testing.T, tm *TaskManager, id string) *Task {
	t.Helper()

	task, err := tm.Wait(id, 5*time.Second)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}
	return task
}

func waitForTaskTimeout(t *testing.T, tm *TaskManager, id string, timeout time.Duration) *Task {
	t.Helper()

	task, err := tm.Wait(id, timeout)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}
	return task
}

func TestTaskManagerStart(t *testing.T) {
	tm := newTestTaskManager()

	task, err := tm.Start("echo hello", "")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if task.ID == "" {
		t.Fatal("task ID should not be empty")
	}
	if task.Status != TaskRunning {
		t.Fatalf("expected status running, got %s", task.Status)
	}
	if task.PID == 0 {
		t.Fatal("task PID should not be 0")
	}
	if task.PGID == 0 {
		t.Fatal("task PGID should not be 0")
	}

	task = waitForTask(t, tm, task.ID)
	if task.Status != TaskCompleted {
		t.Fatalf("expected status completed, got %s", task.Status)
	}
}

func TestTaskManagerGet(t *testing.T) {
	tm := newTestTaskManager()

	task, err := tm.Start("echo hello", "")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	got, ok := tm.Get(task.ID)
	if !ok {
		t.Fatal("task not found")
	}
	if got.ID != task.ID {
		t.Fatalf("expected task ID %s, got %s", task.ID, got.ID)
	}

	_, ok = tm.Get("nonexistent")
	if ok {
		t.Fatal("expected task not to be found")
	}
}

func TestTaskManagerList(t *testing.T) {
	tm := newTestTaskManager()

	task1, _ := tm.Start("echo one", "")
	task2, _ := tm.Start("echo two", "")

	waitForTask(t, tm, task1.ID)
	waitForTask(t, tm, task2.ID)

	all := tm.List("")
	if len(all) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(all))
	}

	completed := tm.List(string(TaskCompleted))
	if len(completed) != 2 {
		t.Fatalf("expected 2 completed tasks, got %d", len(completed))
	}

	running := tm.List(string(TaskRunning))
	if len(running) != 0 {
		t.Fatalf("expected 0 running tasks, got %d", len(running))
	}
}

func TestTaskManagerWait(t *testing.T) {
	tm := newTestTaskManager()

	task, err := tm.Start("echo hello", "")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	result, err := tm.Wait(task.ID, 5*time.Second)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}
	if result.Status != TaskCompleted {
		t.Fatalf("expected status completed, got %s", result.Status)
	}
	if !strings.Contains(result.Output, "hello") {
		t.Fatalf("expected output to contain 'hello', got %q", result.Output)
	}
}

func TestTaskManagerWaitTimeout(t *testing.T) {
	tm := newTestTaskManager()

	task, err := tm.Start("sleep 10", "")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer tm.Kill(task.ID)

	_, err = tm.Wait(task.ID, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestTaskManagerWaitNotFound(t *testing.T) {
	tm := newTestTaskManager()

	_, err := tm.Wait("nonexistent", time.Second)
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestTaskManagerKill(t *testing.T) {
	tm := newTestTaskManager()

	task, err := tm.Start("sleep 60", "")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	err = tm.Kill(task.ID)
	if err != nil {
		t.Fatalf("Kill failed: %v", err)
	}

	got := waitForTask(t, tm, task.ID)
	if got.Status != TaskKilled {
		t.Fatalf("expected status killed, got %s", got.Status)
	}
}

func TestTaskManagerKillProcessGroup(t *testing.T) {
	tm := newTestTaskManager()
	pidFile, err := os.CreateTemp("", "friday-bg-child-*.pid")
	if err != nil {
		t.Fatalf("CreateTemp failed: %v", err)
	}
	pidFile.Close()
	defer os.Remove(pidFile.Name())

	task, err := tm.Start(fmt.Sprintf("sh -c 'sleep 60 & child=$!; echo $child > %q; wait'", pidFile.Name()), "")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	childPID := waitForPIDFile(t, pidFile.Name())

	if err := tm.Kill(task.ID); err != nil {
		t.Fatalf("Kill failed: %v", err)
	}

	waitForTask(t, tm, task.ID)
	waitForProcessExit(t, childPID, 3*time.Second)
}

func waitForPIDFile(t *testing.T, path string) int {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		out, err := os.ReadFile(path)
		if err == nil {
			fields := strings.Fields(string(out))
			if len(fields) > 0 {
				childPID, convErr := strconv.Atoi(fields[0])
				if convErr == nil {
					return childPID
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for pid file %s", path)
	return 0
}

func waitForProcessExit(t *testing.T, pid int, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if err != nil {
			if err == syscall.ESRCH {
				return
			}
			t.Fatalf("unexpected error probing process %d: %v", pid, err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("process %d still alive after timeout", pid)
}

func TestTaskManagerKillNotFound(t *testing.T) {
	tm := newTestTaskManager()

	err := tm.Kill("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestTaskManagerKillNotRunning(t *testing.T) {
	tm := newTestTaskManager()

	task, _ := tm.Start("echo done", "")
	waitForTask(t, tm, task.ID)

	err := tm.Kill(task.ID)
	if err == nil {
		t.Fatal("expected error for non-running task")
	}
}

func TestTaskManagerKillAll(t *testing.T) {
	tm := newTestTaskManager()

	task1, _ := tm.Start("sleep 60", "")
	task2, _ := tm.Start("sleep 60", "")
	defer tm.KillAll()

	tm.KillAll()

	got1 := waitForTaskTimeout(t, tm, task1.ID, 5*time.Second)
	got2 := waitForTaskTimeout(t, tm, task2.ID, 5*time.Second)

	if got1.Status != TaskKilled {
		t.Fatalf("expected task1 status killed, got %s", got1.Status)
	}
	if got2.Status != TaskKilled {
		t.Fatalf("expected task2 status killed, got %s", got2.Status)
	}
}

func TestTaskManagerPermissionDenied(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Enabled = false
	cfg.Permissions.Allow = []string{}
	cfg.Permissions.Deny = []string{"sudo"}
	exec := NewExecutor(cfg)
	tm := NewTaskManager(exec)

	_, err := tm.Start("echo hello", "")
	if err == nil {
		t.Fatal("expected permission denied error")
	}
}

func TestTaskManagerFailedTask(t *testing.T) {
	tm := newTestTaskManager()

	task, err := tm.Start("false", "")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	task = waitForTask(t, tm, task.ID)
	if task.Status != TaskFailed {
		t.Fatalf("expected status failed, got %s", task.Status)
	}
	if task.ExitCode == 0 {
		t.Fatal("expected non-zero exit code")
	}
}

func TestTaskManagerOutputTruncation(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	tm := newTestTaskManager()

	var script strings.Builder
	for i := 0; i < MaxOutputLines+10; i++ {
		script.WriteString("echo line\n")
	}

	task, err := tm.Start(script.String(), "")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	task = waitForTaskTimeout(t, tm, task.ID, 10*time.Second)
	lines := strings.Split(task.Output, "\n")
	if len(lines) > MaxOutputLines+10 {
		t.Fatalf("expected output to be truncated to ~%d lines, got %d", MaxOutputLines, len(lines))
	}
}

func TestTaskManagerCollectsTrailingOutput(t *testing.T) {
	tm := newTestTaskManager()

	task, err := tm.Start("sh -c 'echo first; echo second 1>&2; echo third'", "")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	task = waitForTask(t, tm, task.ID)
	for _, expected := range []string{"first", "second", "third"} {
		if !strings.Contains(task.Output, expected) {
			t.Fatalf("expected output to contain %q, got %q", expected, task.Output)
		}
	}
}

func TestTaskManagerScannerErrorIncludedInOutput(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	tm := newTestTaskManager()
	scriptFile, err := os.CreateTemp("", "friday-bg-long-line-*.py")
	if err != nil {
		t.Fatalf("CreateTemp failed: %v", err)
	}
	defer os.Remove(scriptFile.Name())

	longLine := strings.Repeat("x", 1024*1024+32)
	if _, err := scriptFile.WriteString(fmt.Sprintf("import sys\nsys.stdout.write(%q)\n", longLine)); err != nil {
		scriptFile.Close()
		t.Fatalf("WriteString failed: %v", err)
	}
	if err := scriptFile.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	task, err := tm.Start(fmt.Sprintf("python3 %q", scriptFile.Name()), "")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	task = waitForTaskTimeout(t, tm, task.ID, 10*time.Second)
	if !strings.Contains(task.Output, "[output collection error:") {
		t.Fatalf("expected output collection error in output, got %q", task.Output)
	}
}

func TestTaskManagerWorkdir(t *testing.T) {
	tm := newTestTaskManager()

	dir, _ := os.Getwd()
	task, err := tm.Start("pwd", "")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	task = waitForTask(t, tm, task.ID)
	if !strings.Contains(task.Output, dir) {
		t.Fatalf("expected output to contain %q, got %q", dir, task.Output)
	}
}

func TestGenerateTaskID(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := generateTaskID()
		if ids[id] {
			t.Fatalf("duplicate task ID: %s", id)
		}
		ids[id] = true
	}
}
