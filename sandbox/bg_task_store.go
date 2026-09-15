package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	coresession "github.com/basenana/friday/core/session"
)

const (
	backgroundTasksNamespace = "background_tasks"
	backgroundTasksVersion   = 1
	terminalTaskRetention    = 20
)

type taskRecords interface {
	ReadRecord(context.Context, string) ([]byte, error)
	UpdateRecord(context.Context, string, func([]byte) ([]byte, error)) error
}

// TaskStore persists background task snapshots for one session.
type TaskStore interface {
	Load(context.Context) ([]*Task, error)
	Upsert(context.Context, *Task) error
}

type sessionTaskStore struct{ records taskRecords }

type backgroundTaskRecord struct {
	Version int     `json:"version"`
	Tasks   []*Task `json:"tasks"`
}

// NewSessionTaskStore stores background task snapshots in the session's
// namespaced record store.
func NewSessionTaskStore(records taskRecords) TaskStore {
	return &sessionTaskStore{records: records}
}

func (s *sessionTaskStore) Load(ctx context.Context) ([]*Task, error) {
	if s == nil || s.records == nil {
		return nil, nil
	}
	raw, err := s.records.ReadRecord(ctx, backgroundTasksNamespace)
	if errors.Is(err, coresession.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	record, err := decodeBackgroundTaskRecord(raw)
	if err != nil {
		return nil, err
	}
	return cloneTasks(record.Tasks), nil
}

func (s *sessionTaskStore) Upsert(ctx context.Context, task *Task) error {
	if s == nil || s.records == nil || task == nil {
		return nil
	}
	return s.records.UpdateRecord(ctx, backgroundTasksNamespace, func(raw []byte) ([]byte, error) {
		record, err := decodeBackgroundTaskRecord(raw)
		if err != nil {
			return nil, err
		}
		updated := false
		for i, existing := range record.Tasks {
			if existing != nil && existing.ID == task.ID {
				record.Tasks[i] = cloneTask(task)
				updated = true
				break
			}
		}
		if !updated {
			record.Tasks = append(record.Tasks, cloneTask(task))
		}
		record.Tasks = retainTasks(record.Tasks, terminalTaskRetention)
		record.Version = backgroundTasksVersion
		return json.Marshal(record)
	})
}

func decodeBackgroundTaskRecord(raw []byte) (backgroundTaskRecord, error) {
	if len(raw) == 0 {
		return backgroundTaskRecord{Version: backgroundTasksVersion}, nil
	}
	var record backgroundTaskRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return backgroundTaskRecord{}, err
	}
	if record.Version != backgroundTasksVersion {
		return backgroundTaskRecord{}, errors.New("unsupported background task record version")
	}
	return record, nil
}

func retainTasks(tasks []*Task, terminalLimit int) []*Task {
	var running, terminal []*Task
	for _, task := range tasks {
		if task == nil || task.ID == "" {
			continue
		}
		if task.Status == TaskRunning {
			running = append(running, task)
		} else {
			terminal = append(terminal, task)
		}
	}
	sort.SliceStable(terminal, func(i, j int) bool {
		return taskSortTime(terminal[i]).After(taskSortTime(terminal[j]))
	})
	if terminalLimit >= 0 && len(terminal) > terminalLimit {
		terminal = terminal[:terminalLimit]
	}
	return append(running, terminal...)
}

func taskSortTime(task *Task) time.Time {
	if task != nil && task.FinishedAt != nil {
		return *task.FinishedAt
	}
	if task != nil {
		return task.StartedAt
	}
	return time.Time{}
}

func cloneTask(task *Task) *Task {
	if task == nil {
		return nil
	}
	copy := *task
	if task.FinishedAt != nil {
		finished := *task.FinishedAt
		copy.FinishedAt = &finished
	}
	return &copy
}

func cloneTasks(tasks []*Task) []*Task {
	result := make([]*Task, 0, len(tasks))
	for _, task := range tasks {
		if cloned := cloneTask(task); cloned != nil {
			result = append(result, cloned)
		}
	}
	return result
}
