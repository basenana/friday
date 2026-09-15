package sandbox

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	coresession "github.com/basenana/friday/core/session"
)

func TestPersistentTaskManagerRestoresCompletedTask(t *testing.T) {
	sess := coresession.New("task-session", nil)
	store := NewSessionTaskStore(sess)
	tm, err := NewPersistentTaskManager(newTestTaskManager().exec, store)
	if err != nil {
		t.Fatal(err)
	}
	task, err := tm.Start("echo persisted-output", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	completed := waitForTask(t, tm, task.ID)
	if completed.Status != TaskCompleted {
		t.Fatalf("completed status = %q", completed.Status)
	}

	restored, err := NewPersistentTaskManager(newTestTaskManager().exec, store)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := restored.Get(task.ID)
	if !ok {
		t.Fatalf("completed task %q was not restored", task.ID)
	}
	if got.Status != TaskCompleted || !strings.Contains(got.Output, "persisted-output") {
		t.Fatalf("restored task = %+v", got)
	}
}

func TestPersistentTaskManagerMarksStaleRunningTaskInterrupted(t *testing.T) {
	sess := coresession.New("task-session", nil)
	store := NewSessionTaskStore(sess)
	started := time.Now().Add(-time.Minute)
	if err := store.Upsert(context.Background(), &Task{
		ID: "stale-running", Command: "sleep 60", PID: 123, PGID: 123,
		Status: TaskRunning, StartedAt: started,
	}); err != nil {
		t.Fatal(err)
	}

	tm, err := NewPersistentTaskManager(nil, store)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := tm.Get("stale-running")
	if !ok {
		t.Fatal("stale task was not restored")
	}
	if got.Status != TaskInterrupted || got.ExitCode != -1 || got.FinishedAt == nil {
		t.Fatalf("restored stale task = %+v", got)
	}
	if got.Output != "[task interrupted by previous Friday process]" {
		t.Fatalf("interrupted output = %q", got.Output)
	}
	persisted, err := store.Load(context.Background())
	if err != nil || len(persisted) != 1 || persisted[0].Status != TaskInterrupted {
		t.Fatalf("persisted tasks = %+v, err = %v", persisted, err)
	}
}

func TestSessionTaskStoreRetainsRunningAndLatestTerminalTasks(t *testing.T) {
	sess := coresession.New("task-session", nil)
	store := NewSessionTaskStore(sess)
	base := time.Now().Add(-time.Hour)
	for i := 0; i < terminalTaskRetention+5; i++ {
		finished := base.Add(time.Duration(i) * time.Minute)
		if err := store.Upsert(context.Background(), &Task{
			ID: fmt.Sprintf("done-%02d", i), Status: TaskCompleted,
			StartedAt: finished.Add(-time.Second), FinishedAt: &finished,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := store.Upsert(context.Background(), &Task{
			ID: fmt.Sprintf("running-%d", i), Status: TaskRunning, StartedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	tasks, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != terminalTaskRetention+2 {
		t.Fatalf("retained %d tasks, want %d", len(tasks), terminalTaskRetention+2)
	}
	seen := make(map[string]bool, len(tasks))
	for _, task := range tasks {
		seen[task.ID] = true
	}
	for i := 0; i < 5; i++ {
		if seen[fmt.Sprintf("done-%02d", i)] {
			t.Fatalf("old terminal task done-%02d was retained", i)
		}
	}
	for i := 0; i < 2; i++ {
		if !seen[fmt.Sprintf("running-%d", i)] {
			t.Fatalf("running task %d was pruned", i)
		}
	}
}
