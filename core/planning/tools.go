package planning

import (
	"bytes"
	"context"
	"fmt"
	"strconv"

	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
)

func writeTodoListHandler(t *Todo, sess *session.Session) tools.ToolHandlerFunc {
	return func(ctx context.Context, request *tools.Request) (*tools.Result, error) {
		todoList, ok := request.Arguments["todo_list"].([]any)
		if !ok {
			return tools.NewToolResultError("missing required parameter: todo_list"), nil
		}

		todo := &TodoList{}
		for _, todoItem := range todoList {
			todoInfo, ok := todoItem.(map[string]interface{})
			if !ok || len(todoInfo) == 0 {
				return tools.NewToolResultError("invalid todo_list format"), nil
			}

			describe, ok := todoInfo["describe"].(string)
			if !ok || describe == "" {
				return tools.NewToolResultError("invalid todo_list format: describe is required"), nil
			}
			status, ok := todoInfo["status"].(string)
			if !ok || status == "" {
				return tools.NewToolResultError("invalid todo_list format: status is required"), nil
			}

			todo.Todos = append(todo.Todos, &TodoItem{Describe: describe, Status: status})
		}

		key := todoStateKey(sess)
		t.mu.Lock()
		t.todoMaps[key] = todo
		t.mu.Unlock()

		var pending, inProgress, completed int
		for _, item := range todo.Todos {
			switch item.Status {
			case "pending":
				pending++
			case "in_progress":
				inProgress++
			case "completed":
				completed++
			}
		}
		sess.PublishEvent(types.Event{
			Type: types.EventTodoUpdate,
			Data: map[string]string{
				"count":       strconv.Itoa(len(todo.Todos)),
				"pending":     strconv.Itoa(pending),
				"in_progress": strconv.Itoa(inProgress),
				"completed":   strconv.Itoa(completed),
			},
		})

		return tools.NewToolResultText(fmt.Sprintf("Updated todo list to:\n %s", displayTodoList(todo))), nil
	}
}

type TodoList struct {
	Todos []*TodoItem `json:"todos"`
}

type TodoItem struct {
	Describe string `json:"describe"`
	Status   string `json:"status"`
}

func displayTodoList(todo *TodoList) string {
	buf := &bytes.Buffer{}
	buf.WriteString("<current_todo_list>\n")
	todoList := todo.Todos
	if len(todoList) > 0 {
		for _, t := range todoList {
			buf.WriteString(fmt.Sprintf("describe=%s status=%v\n", t.Describe, t.Status))
		}
	} else {
		buf.WriteString("[EMPTY]\n")
	}
	buf.WriteString("</current_todo_list>\n")
	return buf.String()
}

func todoStateKey(sess *session.Session) string {
	return fmt.Sprintf("todo_list_%s_%s", sess.Root.ID, sess.ID)
}
