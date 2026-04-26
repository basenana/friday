package planning

import (
	"context"
	"testing"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
)

func TestBeforeModel_EmptyHistory(t *testing.T) {
	todo := New(Option{})
	sess := session.New(types.NewID(), nil)
	req := providers.NewRequest("")

	err := todo.BeforeModel(context.Background(), sess, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	history := req.History()
	if len(history) != 0 {
		t.Fatalf("expected empty history, got %d messages", len(history))
	}
}

func TestBeforeModel_SliceAliasing(t *testing.T) {
	todo := New(Option{})
	sess := session.New(types.NewID(), nil)

	original := []types.Message{
		{Role: types.RoleUser, Content: "msg1"},
		{Role: types.RoleUser, Content: "msg2"},
	}
	req := providers.NewRequest("", original...)

	// Set up a todo list so BeforeModel mutates history
	todo.todoMaps[todoStateKey(sess)] = &TodoList{
		Todos: []*TodoItem{{Describe: "task1", Status: "pending"}},
	}

	err := todo.BeforeModel(context.Background(), sess, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Original slice should remain unchanged
	if len(original) != 2 {
		t.Fatalf("original slice was mutated: length changed from 2 to %d", len(original))
	}
	if original[0].Content != "msg1" || original[1].Content != "msg2" {
		t.Fatalf("original slice contents were mutated")
	}
}

func TestBeforeModel_InsertsBeforeLastUserMessage(t *testing.T) {
	todo := New(Option{})
	sess := session.New(types.NewID(), nil)

	original := []types.Message{
		{Role: types.RoleUser, Content: "msg1"},
		{Role: types.RoleUser, Content: "msg2"},
	}
	req := providers.NewRequest("", original...)

	todo.todoMaps[todoStateKey(sess)] = &TodoList{
		Todos: []*TodoItem{{Describe: "task1", Status: "pending"}},
	}

	err := todo.BeforeModel(context.Background(), sess, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	history := req.History()
	if len(history) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(history))
	}
	if history[0].Content != "msg1" {
		t.Fatalf("expected first message to be msg1, got %s", history[0].Content)
	}
	if history[1].Role != types.RoleAgent {
		t.Fatalf("expected second message to be agent role, got %s", history[1].Role)
	}
	if history[2].Content != "msg2" {
		t.Fatalf("expected third message to be msg2, got %s", history[2].Content)
	}
}

func TestBeforeModel_AppendsAfterToolMessage(t *testing.T) {
	todo := New(Option{})
	sess := session.New(types.NewID(), nil)

	original := []types.Message{
		{Role: types.RoleTool, Content: "tool result"},
	}
	req := providers.NewRequest("", original...)

	todo.todoMaps[todoStateKey(sess)] = &TodoList{
		Todos: []*TodoItem{{Describe: "task1", Status: "pending"}},
	}

	err := todo.BeforeModel(context.Background(), sess, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	history := req.History()
	if len(history) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(history))
	}
	if history[0].Role != types.RoleTool {
		t.Fatalf("expected first message to be tool role, got %s", history[0].Role)
	}
	if history[1].Role != types.RoleAgent {
		t.Fatalf("expected second message to be agent role, got %s", history[1].Role)
	}
}

func TestRemoveTodo(t *testing.T) {
	todo := New(Option{})
	sess := session.New(types.NewID(), nil)
	key := todoStateKey(sess)

	todo.todoMaps[key] = &TodoList{
		Todos: []*TodoItem{{Describe: "task1", Status: "pending"}},
	}

	if _, ok := todo.todoMaps[key]; !ok {
		t.Fatal("expected todo to exist before removal")
	}

	todo.RemoveTodo(sess)

	if _, ok := todo.todoMaps[key]; ok {
		t.Fatal("expected todo to be removed")
	}
}

func TestNew_DefaultPrompts(t *testing.T) {
	todo := New(Option{})

	if todo.opt.SystemPrompt != DEFAULT_PLANNING_PROMPT {
		t.Fatalf("expected default system prompt, got %s", todo.opt.SystemPrompt)
	}
	if todo.opt.TaskDescribePrompt != DEFAULT_TASK_DESC_PROMPT {
		t.Fatalf("expected default task describe prompt, got %s", todo.opt.TaskDescribePrompt)
	}
}
