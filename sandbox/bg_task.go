package sandbox

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/basenana/friday/core/tools"
)

type TaskStatus string

const (
	TaskRunning   TaskStatus = "running"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
	TaskKilled    TaskStatus = "killed"
)

type Task struct {
	ID         string
	Command    string
	Workdir    string
	PID        int
	PGID       int
	Status     TaskStatus
	StartedAt  time.Time
	FinishedAt *time.Time
	ExitCode   int
	Output     string
}

type managedTask struct {
	Task
	done chan struct{}
}

type TaskManager struct {
	mu    sync.RWMutex
	tasks map[string]*managedTask
	exec  *Executor
}

func NewTaskManager(exec *Executor) *TaskManager {
	return &TaskManager{
		tasks: make(map[string]*managedTask),
		exec:  exec,
	}
}

func generateTaskID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (tm *TaskManager) Start(command, workdir string) (*Task, error) {
	decision, reason, err := tm.exec.CheckPermission(command)
	if err != nil {
		return nil, fmt.Errorf("permission check failed: %w", err)
	}
	if decision == Deny {
		return nil, fmt.Errorf("permission denied: %s", reason)
	}

	dir, err := ValidateWorkdir(workdir)
	if err != nil {
		return nil, fmt.Errorf("invalid workdir: %w", err)
	}

	opts := ExecOptions{Workdir: dir}
	wrappedCmd, cleanup, err := tm.exec.WrapCommand(command, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to wrap command: %w", err)
	}

	cmd := exec.Command("bash", "-c", wrappedCmd)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start command: %w", err)
	}

	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		pgid = cmd.Process.Pid
	}

	task := &managedTask{
		Task: Task{
			Command:   command,
			Workdir:   dir,
			PID:       cmd.Process.Pid,
			PGID:      pgid,
			Status:    TaskRunning,
			StartedAt: time.Now(),
		},
		done: make(chan struct{}),
	}

	tm.mu.Lock()
	for {
		task.ID = generateTaskID()
		if _, exists := tm.tasks[task.ID]; !exists {
			break
		}
	}
	tm.tasks[task.ID] = task
	tm.mu.Unlock()

	var readers sync.WaitGroup
	collector := &outputCollector{}

	readers.Add(2)
	go collectOutput(stdout, collector, &readers)
	go collectOutput(stderr, collector, &readers)

	go func() {
		defer close(task.done)
		if cleanup != nil {
			defer cleanup()
		}

		waitErr := cmd.Wait()
		readers.Wait()
		output := collector.Output()

		tm.mu.Lock()
		defer tm.mu.Unlock()

		task.Output = output
		task.ExitCode = exitCodeFromCmd(cmd, waitErr)
		now := time.Now()
		task.FinishedAt = &now

		if task.Status == TaskKilled {
			return
		}

		if waitErr != nil {
			task.Status = TaskFailed
			return
		}

		task.Status = TaskCompleted
	}()

	return snapshotTask(task), nil
}

type outputCollector struct {
	mu    sync.Mutex
	lines []string
}

func (c *outputCollector) appendLine(line string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.lines = append(c.lines, line)
	if len(c.lines) > MaxOutputLines {
		c.lines = c.lines[1:]
	}
}

func (c *outputCollector) Output() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return truncateOutput(strings.Join(c.lines, "\n"))
}

func collectOutput(reader io.Reader, collector *outputCollector, wg *sync.WaitGroup) {
	defer wg.Done()

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		collector.appendLine(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		collector.appendLine(fmt.Sprintf("[output collection error: %v]", err))
	}
}

func snapshotTask(task *managedTask) *Task {
	snapshot := task.Task
	if task.FinishedAt != nil {
		finishedAt := *task.FinishedAt
		snapshot.FinishedAt = &finishedAt
	}
	return &snapshot
}

func (tm *TaskManager) List(status string) []*Task {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	var result []*Task
	for _, t := range tm.tasks {
		if status == "" || string(t.Status) == status {
			result = append(result, snapshotTask(t))
		}
	}
	return result
}

func (tm *TaskManager) Get(id string) (*Task, bool) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	t, ok := tm.tasks[id]
	if !ok {
		return nil, false
	}
	return snapshotTask(t), true
}

func (tm *TaskManager) taskByID(id string) (*managedTask, bool) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	task, ok := tm.tasks[id]
	return task, ok
}

func (tm *TaskManager) taskStatus(id string) (TaskStatus, bool) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	task, ok := tm.tasks[id]
	if !ok {
		return "", false
	}
	return task.Status, true
}

func (tm *TaskManager) Kill(id string) error {
	task, ok := tm.taskByID(id)
	if !ok {
		return fmt.Errorf("task not found: %s", id)
	}

	tm.mu.Lock()
	if task.Status != TaskRunning {
		tm.mu.Unlock()
		return fmt.Errorf("task %s is not running", id)
	}
	task.Status = TaskKilled
	now := time.Now()
	task.FinishedAt = &now
	tm.mu.Unlock()

	_ = signalTaskGroup(task.PGID, syscall.SIGTERM)

	select {
	case <-task.done:
		return nil
	case <-time.After(2 * time.Second):
	}

	_ = signalTaskGroup(task.PGID, syscall.SIGKILL)

	select {
	case <-task.done:
	case <-time.After(2 * time.Second):
	}

	return nil
}

func signalTaskGroup(pgid int, sig syscall.Signal) error {
	if pgid <= 0 {
		return fmt.Errorf("invalid pgid: %d", pgid)
	}
	return syscall.Kill(-pgid, sig)
}

func exitCodeFromCmd(cmd *exec.Cmd, waitErr error) int {
	if cmd != nil && cmd.ProcessState != nil {
		return cmd.ProcessState.ExitCode()
	}
	if exitErr, ok := waitErr.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	if waitErr != nil {
		return 1
	}
	return 0
}

func (tm *TaskManager) Wait(id string, timeout time.Duration) (*Task, error) {
	task, ok := tm.taskByID(id)
	if !ok {
		return nil, fmt.Errorf("task not found: %s", id)
	}
	status, ok := tm.taskStatus(id)
	if !ok {
		return nil, fmt.Errorf("task not found: %s", id)
	}
	if status != TaskRunning {
		snapshot, _ := tm.Get(id)
		return snapshot, nil
	}

	select {
	case <-task.done:
		snapshot, _ := tm.Get(id)
		return snapshot, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout waiting for task %s", id)
	}
}

func (tm *TaskManager) KillAll() {
	tm.mu.RLock()
	ids := make([]string, 0, len(tm.tasks))
	for _, task := range tm.tasks {
		if task.Status == TaskRunning {
			ids = append(ids, task.ID)
		}
	}
	tm.mu.RUnlock()

	for _, id := range ids {
		_ = tm.Kill(id)
	}
}

func NewBackgroundTaskTools(tm *TaskManager, workdir string) []*tools.Tool {
	return []*tools.Tool{
		newBackgroundTaskTool(tm, workdir),
		newListTasksTool(tm),
		newKillTaskTool(tm),
		newWaitTaskTool(tm),
	}
}

func newBackgroundTaskTool(tm *TaskManager, workdir string) *tools.Tool {
	return tools.NewTool("background_task",
		tools.WithDescription(fmt.Sprintf(`Run a command in the background and return immediately with a task ID.

Current working directory: %s

The command runs asynchronously. Use list_tasks to check status, wait_task to get output, or kill_task to terminate.

Commands are executed with the same safety restrictions as the bash tool:
- Commands must be in the allow list
- Dangerous commands are blocked
- File system and network access may be restricted`, workdir)),
		tools.WithString("command", tools.Required(), tools.Description("The shell command to execute")),
		tools.WithString("workdir", tools.Description("Working directory for the command")),
		tools.WithToolHandler(backgroundTaskHandler(tm, workdir)),
	)
}

func backgroundTaskHandler(tm *TaskManager, defaultWorkdir string) tools.ToolHandlerFunc {
	return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
		command, ok := req.Arguments["command"].(string)
		if !ok || command == "" {
			return tools.NewToolResultError("command is required"), nil
		}

		workdir := defaultWorkdir
		if w, ok := req.Arguments["workdir"].(string); ok && w != "" {
			workdir = w
		}

		task, err := tm.Start(command, workdir)
		if err != nil {
			return tools.NewToolResultError(err.Error()), nil
		}

		return tools.NewToolResultText(fmt.Sprintf(
			"Started background task %s\nPID: %d\nCommand: %s",
			task.ID, task.PID, task.Command,
		)), nil
	}
}

func newListTasksTool(tm *TaskManager) *tools.Tool {
	return tools.NewTool("list_tasks",
		tools.WithDescription(`List all background tasks with their status.

Returns a table showing task ID, status, command, PID, and duration.`),
		tools.WithString("status", tools.Description("Filter by status: running, completed, failed, killed"), tools.Enum("running", "completed", "failed", "killed")),
		tools.WithToolHandler(listTasksHandler(tm)),
	)
}

func listTasksHandler(tm *TaskManager) tools.ToolHandlerFunc {
	return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
		var status string
		if s, ok := req.Arguments["status"].(string); ok {
			status = s
		}

		tasks := tm.List(status)
		if len(tasks) == 0 {
			return tools.NewToolResultText("No tasks found."), nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("%-8s %-12s %-6s %-8s %s\n", "ID", "STATUS", "PID", "DURATION", "COMMAND"))
		for _, t := range tasks {
			duration := time.Since(t.StartedAt).Truncate(time.Second).String()
			if t.FinishedAt != nil {
				duration = t.FinishedAt.Sub(t.StartedAt).Truncate(time.Second).String()
			}
			cmd := t.Command
			if len(cmd) > 40 {
				cmd = cmd[:37] + "..."
			}
			sb.WriteString(fmt.Sprintf("%-8s %-12s %-6d %-8s %s\n", t.ID, t.Status, t.PID, duration, cmd))
		}

		return tools.NewToolResultText(sb.String()), nil
	}
}

func newKillTaskTool(tm *TaskManager) *tools.Tool {
	return tools.NewTool("kill_task",
		tools.WithDescription("Kill a running background task. Sends SIGTERM, then SIGKILL after 2 seconds if still running."),
		tools.WithString("task_id", tools.Required(), tools.Description("The task ID to kill")),
		tools.WithToolHandler(killTaskHandler(tm)),
	)
}

func killTaskHandler(tm *TaskManager) tools.ToolHandlerFunc {
	return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
		taskID, ok := req.Arguments["task_id"].(string)
		if !ok || taskID == "" {
			return tools.NewToolResultError("task_id is required"), nil
		}

		if err := tm.Kill(taskID); err != nil {
			return tools.NewToolResultError(err.Error()), nil
		}

		return tools.NewToolResultText(fmt.Sprintf("Task %s killed", taskID)), nil
	}
}

func newWaitTaskTool(tm *TaskManager) *tools.Tool {
	return tools.NewTool("wait_task",
		tools.WithDescription(`Wait for a background task to complete and return its output.

Returns immediately if the task is not running. Default timeout is 60s.`),
		tools.WithString("task_id", tools.Required(), tools.Description("The task ID to wait for")),
		tools.WithString("timeout", tools.Description("Timeout duration (e.g., '30s', '5m'). Default is 60s.")),
		tools.WithToolHandler(waitTaskHandler(tm)),
	)
}

func waitTaskHandler(tm *TaskManager) tools.ToolHandlerFunc {
	return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
		taskID, ok := req.Arguments["task_id"].(string)
		if !ok || taskID == "" {
			return tools.NewToolResultError("task_id is required"), nil
		}

		timeout := 60 * time.Second
		if t, ok := req.Arguments["timeout"].(string); ok && t != "" {
			d, err := parseDuration(t)
			if err != nil {
				return tools.NewToolResultError(fmt.Sprintf("invalid timeout: %v", err)), nil
			}
			timeout = d
		}

		task, err := tm.Wait(taskID, timeout)
		if err != nil {
			return tools.NewToolResultError(err.Error()), nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Task %s\n", task.ID))
		sb.WriteString(fmt.Sprintf("Status: %s\n", task.Status))
		sb.WriteString(fmt.Sprintf("Exit code: %d\n", task.ExitCode))
		if task.Output != "" {
			sb.WriteString("Output:\n")
			sb.WriteString(task.Output)
		}

		return tools.NewToolResultText(sb.String()), nil
	}
}
