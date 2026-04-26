package planning

import (
	"context"
	"sync"

	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
)

type Todo struct {
	todoMaps map[string]*TodoList
	opt      Option
	mu       sync.RWMutex
	logger   logger.Logger
}

var _ session.BeforeAgentHook = &Todo{}
var _ session.BeforeModelHook = &Todo{}

func (a *Todo) BeforeAgent(ctx context.Context, sess *session.Session, req session.AgentRequest) error {
	req.AppendTools(a.planningTools(sess)...)
	return nil
}

func (a *Todo) BeforeModel(ctx context.Context, sess *session.Session, req providers.Request) error {
	req.AppendSystemPrompt(a.opt.SystemPrompt)

	key := todoStateKey(sess)
	a.mu.RLock()
	todo := a.todoMaps[key]
	a.mu.RUnlock()

	if todo == nil || len(todo.Todos) == 0 {
		return nil
	}

	messages := req.History()
	if len(messages) == 0 {
		return nil
	}
	lastMessage := messages[len(messages)-1]
	if lastMessage.Role == types.RoleTool || lastMessage.IsToolCall() {
		messages = append(messages, types.Message{Role: types.RoleAgent, Content: displayTodoList(todo)})
		req.SetHistory(messages)
		return nil
	}

	newMessage := make([]types.Message, len(messages)-1, len(messages)+1)
	copy(newMessage, messages[:len(messages)-1])
	newMessage = append(newMessage, types.Message{Role: types.RoleAgent, Content: displayTodoList(todo)})
	newMessage = append(newMessage, lastMessage)
	req.SetHistory(newMessage)
	return nil
}

func (a *Todo) planningTools(sess *session.Session) []*tools.Tool {
	return []*tools.Tool{
		tools.NewTool(
			"write_todos",
			tools.WithDescription(a.opt.TaskDescribePrompt),
			tools.WithArray("todo_list",
				tools.Required(),
				tools.Items(map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"describe": map[string]interface{}{
							"type":        "string",
							"description": "Describe the task to be performed and how to determine the task's outcome in one sentence.",
						},
						"status": map[string]interface{}{
							"type":        "string",
							"description": "Task status: pending, in_progress, or completed.",
							"enum":        []string{"pending", "in_progress", "completed"},
						},
					},
					"required": []string{"describe", "status"},
				}),
				tools.Description("A list of tasks, each task is an object with 'describe' and 'status' fields. 'describe' is the task description, 'status' is the current state: pending, in_progress, or completed."),
			),
			tools.WithToolHandler(writeTodoListHandler(a, sess)),
		),
	}
}

func New(option Option) *Todo {
	if option.SystemPrompt == "" {
		option.SystemPrompt = DEFAULT_PLANNING_PROMPT
	}

	if option.TaskDescribePrompt == "" {
		option.TaskDescribePrompt = DEFAULT_TASK_DESC_PROMPT
	}

	return &Todo{
		opt:      option,
		todoMaps: make(map[string]*TodoList),
		logger:   logger.New("planning"),
	}
}

func (a *Todo) RemoveTodo(sess *session.Session) {
	key := todoStateKey(sess)
	a.mu.Lock()
	delete(a.todoMaps, key)
	a.mu.Unlock()
}

type Option struct {
	SystemPrompt       string
	TaskDescribePrompt string
}
