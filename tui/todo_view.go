package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/basenana/friday/core/actor/events"
	"github.com/charmbracelet/x/ansi"
)

type todoItem struct {
	Description string `json:"description"`
	Status      string `json:"status"`
}

type todoListPayload struct {
	Todos []todoItem `json:"todos"`
}

func (m *model) handleTodoUpdate(evt events.Event) {
	todos, err := decodeTodoUpdate(evt)
	if err != nil {
		if !m.replaying {
			m.appendBlock(chatBlock{kind: blockError, content: "todo update: " + err.Error()})
		}
		return
	}
	m.todos = append([]todoItem(nil), todos...)
}

func decodeTodoUpdate(evt events.Event) ([]todoItem, error) {
	var data events.CustomData
	if err := events.DecodePayload(evt, &data); err != nil {
		return nil, err
	}
	raw, ok := data.Body["todo_list"].(string)
	if !ok {
		return nil, errors.New("missing todo_list")
	}
	var payload todoListPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("invalid todo_list: %w", err)
	}
	for i, item := range payload.Todos {
		if strings.TrimSpace(item.Description) == "" {
			return nil, fmt.Errorf("item %d has no description", i+1)
		}
		if !validTodoStatus(item.Status) {
			return nil, fmt.Errorf("item %d has invalid status %q", i+1, item.Status)
		}
	}
	return payload.Todos, nil
}

func validTodoStatus(status string) bool {
	switch status {
	case "pending", "in_progress", "completed", "blocked":
		return true
	default:
		return false
	}
}

func (m *model) renderTodoPanel(width int) string {
	if len(m.todos) == 0 {
		return ""
	}
	completed := 0
	for _, item := range m.todos {
		if item.Status == "completed" {
			completed++
		}
	}
	if completed == len(m.todos) {
		return ""
	}

	lines := []string{accentStyle.Copy().Bold(true).Render(fmt.Sprintf("Todos · %d/%d", completed, len(m.todos)))}
	contentWidth := max(width-8, 8)
	for _, item := range m.todos {
		icon, style := todoStatusStyle(item.Status)
		wrapped := strings.Split(ansi.Wrap(terminalSafe(item.Description), contentWidth, ""), "\n")
		for i, line := range wrapped {
			prefix := "  "
			if i == 0 {
				prefix = icon + " "
			}
			lines = append(lines, style.Render(prefix+line))
		}
	}
	return toolBoxStyle.Copy().BorderForeground(themeAccent).Render(strings.Join(lines, "\n"))
}

func todoStatusStyle(status string) (string, lipgloss.Style) {
	switch status {
	case "in_progress":
		return "◐", accentStyle.Copy().Bold(true)
	case "completed":
		return "✓", lipgloss.NewStyle().Foreground(themeAdded)
	case "blocked":
		return "!", errorStyle
	default:
		return "○", mutedStyle
	}
}
