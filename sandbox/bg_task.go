package sandbox

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/tools"
)

type TaskStatus string

const (
	TaskRunning     TaskStatus = "running"
	TaskCompleted   TaskStatus = "completed"
	TaskFailed      TaskStatus = "failed"
	TaskKilled      TaskStatus = "killed"
	TaskInterrupted TaskStatus = "interrupted"
)

type Task struct {
	ID         string     `json:"id"`
	Command    string     `json:"command"`
	Workdir    string     `json:"workdir"`
	PID        int        `json:"pid"`
	PGID       int        `json:"pgid"`
	Status     TaskStatus `json:"status"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	ExitCode   int        `json:"exit_code"`
	Output     string     `json:"output,omitempty"`
}

type managedTask struct {
	Task
	done chan struct{}
}

type TaskManager struct {
	mu    sync.RWMutex
	tasks map[string]*managedTask
	exec  *Executor
	store TaskStore
}

// NewPersistentTaskManager restores task snapshots from store. Records left
// running by a previous process are terminalized as interrupted; process IDs
// are never reused for signalling after restart.
func NewPersistentTaskManager(exec *Executor, store TaskStore) (*TaskManager, error) {
	tm := NewTaskManager(exec)
	tm.store = store
	if store == nil {
		return tm, nil
	}
	tasks, err := store.Load(context.Background())
	if err != nil {
		return nil, err
	}
	for _, task := range tasks {
		if task == nil || task.ID == "" {
			continue
		}
		if task.Status == TaskRunning {
			now := time.Now()
			task.Status = TaskInterrupted
			task.FinishedAt = &now
			task.ExitCode = -1
			if task.Output != "" {
				task.Output += "\n"
			}
			task.Output += "[task interrupted by previous Friday process]"
			if err := store.Upsert(context.Background(), task); err != nil {
				return nil, err
			}
		}
		done := make(chan struct{})
		close(done)
		tm.tasks[task.ID] = &managedTask{Task: *cloneTask(task), done: done}
	}
	return tm, nil
}

func (tm *TaskManager) persist(task *Task) error {
	if tm == nil || tm.store == nil || task == nil {
		return nil
	}
	return tm.store.Upsert(context.Background(), task)
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
	cmd.Env = tm.exec.buildCommandEnv(nil, "")
	configureProcessGroup(cmd)

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

	pgid := commandProcessGroupID(cmd)

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

	if err := tm.persist(snapshotTask(task)); err != nil {
		_ = terminateProcessGroup(task.PGID, true)
		readers.Wait()
		_ = cmd.Wait()
		if cleanup != nil {
			cleanup()
		}
		tm.mu.Lock()
		delete(tm.tasks, task.ID)
		tm.mu.Unlock()
		return nil, fmt.Errorf("persist background task: %w", err)
	}

	go func() {
		defer close(task.done)
		if cleanup != nil {
			defer cleanup()
		}

		readers.Wait()
		waitErr := cmd.Wait()
		output := collector.Output()

		tm.mu.Lock()

		task.Output = output
		task.ExitCode = exitCodeFromCmd(cmd, waitErr)
		now := time.Now()
		if task.FinishedAt == nil {
			task.FinishedAt = &now
		}

		if task.Status != TaskKilled {
			if waitErr != nil {
				task.Status = TaskFailed
			} else {
				task.Status = TaskCompleted
			}
		}
		snapshot := snapshotTask(task)
		tm.mu.Unlock()
		if err := tm.persist(snapshot); err != nil {
			logger.New("sandbox.tasks").Warnw("failed to persist completed background task", "task_id", task.ID, "error", err)
		}
	}()

	tm.mu.RLock()
	snapshot := snapshotTask(task)
	tm.mu.RUnlock()
	return snapshot, nil
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
	snapshot := snapshotTask(task)
	tm.mu.Unlock()
	persistErr := tm.persist(snapshot)

	_ = terminateProcessGroup(task.PGID, false)

	select {
	case <-task.done:
		return persistErr
	case <-time.After(2 * time.Second):
	}

	_ = terminateProcessGroup(task.PGID, true)

	select {
	case <-task.done:
	case <-time.After(2 * time.Second):
	}

	return persistErr
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

func (tm *TaskManager) Wait(ctx context.Context, id string) (*Task, error) {
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
	case <-ctx.Done():
		return nil, fmt.Errorf("wait for task %s: %w", id, ctx.Err())
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
			return tools.NewToolResultActionableError("command is required and must be a non-empty string", "provide the background command and retry"), nil
		}

		workdir := defaultWorkdir
		if w, ok := req.Arguments["workdir"].(string); ok && w != "" {
			workdir = w
		}

		task, err := tm.Start(command, workdir)
		if err != nil {
			return tools.NewToolResultActionableError(err.Error(), "use an allowed command and an existing workdir, then retry"), nil
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
		tools.WithString("status", tools.Description("Filter by status: running, completed, failed, killed, interrupted"), tools.Enum("running", "completed", "failed", "killed", "interrupted")),
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
			return tools.NewToolResultActionableError("task_id is required and must be a non-empty string", "call list_tasks, then provide one returned task ID"), nil
		}

		if err := tm.Kill(taskID); err != nil {
			return tools.NewToolResultActionableError(err.Error(), "call list_tasks to verify the task ID and status before retrying"), nil
		}

		return tools.NewToolResultText(fmt.Sprintf("Task %s killed", taskID)), nil
	}
}

func newWaitTaskTool(tm *TaskManager) *tools.Tool {
	return tools.NewTool("wait_task",
		tools.WithDescription(`Wait for a background task to complete and return its output.

Returns immediately if the task is not running. Default timeout is 60s.`),
		tools.WithString("task_id", tools.Required(), tools.Description("The task ID to wait for")),
		tools.WithString("timeout", tools.Description("Timeout duration (e.g., '30s', '5m'), up to 15m. Default is 60s.")),
		tools.WithToolTimeout(60*time.Second, "timeout"),
		tools.WithToolHandler(waitTaskHandler(tm)),
	)
}

func waitTaskHandler(tm *TaskManager) tools.ToolHandlerFunc {
	return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
		taskID, ok := req.Arguments["task_id"].(string)
		if !ok || taskID == "" {
			return tools.NewToolResultActionableError("task_id is required and must be a non-empty string", "call list_tasks, then provide one returned task ID"), nil
		}

		task, err := tm.Wait(ctx, taskID)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return tools.NewToolResultActionableError(err.Error(), "call list_tasks to verify the task ID before retrying"), nil
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
