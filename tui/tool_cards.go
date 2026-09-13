package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const toolCardLineLimit = 10

type toolPresentation struct {
	title       string
	body        string
	specialized bool
}

func (m *model) renderToolCard(block *chatBlock) string {
	presentation := m.presentTool(block)
	style, icon := toolStatusStyle(block)
	title := truncateWidth(terminalSafe(presentation.title), max(m.width-8, 12))
	header := fmt.Sprintf("%s %s", icon, title)
	wrappedBody := ansi.Wrap(presentation.body, max(m.width-6, 10), "")
	body, truncated := truncateToolBody(wrappedBody, toolCardLineLimit)

	lines := []string{header}
	if body != "" {
		lines = append(lines, body)
	}
	if block.interrupted {
		lines = append(lines, mutedStyle.Render("↳ interrupted"))
	} else if block.id != "" && (!block.pending && !block.success && presentation.specialized) {
		lines = append(lines, mutedStyle.Render("/show "+shortID(block.id)+" · error details"))
	} else if block.id != "" && truncated {
		lines = append(lines, mutedStyle.Render("/show "+shortID(block.id)+" · full details"))
	}
	return style.Render(strings.Join(lines, "\n"))
}

func toolStatusStyle(block *chatBlock) (style lipgloss.Style, icon string) {
	base := toolBoxStyle.Copy()
	switch {
	case block.interrupted:
		return base.BorderForeground(themeMuted), "■"
	case block.pending:
		return base.BorderForeground(themeAccent), "…"
	case block.success:
		return base.BorderForeground(themeAdded), "✓"
	default:
		return base.BorderForeground(themeError), "✗"
	}
}

func (m *model) presentTool(block *chatBlock) toolPresentation {
	args, valid := decodeToolArguments(block.toolArgs)
	if presentation, ok := m.presentBuiltinTool(block.toolName, args); ok {
		if !valid && strings.TrimSpace(block.toolArgs) != "" && block.toolName != "write_todos" {
			presentation.body = unavailableToolArguments(block)
		}
		return presentation
	}
	return toolPresentation{
		title: block.toolName,
		body:  genericToolBody(block, args, valid),
	}
}

func (m *model) presentBuiltinTool(name string, args map[string]any) (toolPresentation, bool) {
	value := func(key string) string { return stringValue(args[key]) }
	fields := func(items ...string) string { return strings.Join(nonEmptyStrings(items...), "\n") }
	command := func(title string, extras ...string) toolPresentation {
		items := append([]string{quoteCommand(value("command"))}, extras...)
		body := fields(items...)
		return toolPresentation{title: title, body: body, specialized: true}
	}

	switch name {
	case "write_todos":
		return toolPresentation{title: "Update todos", specialized: true}, true
	case "fs_read":
		return toolPresentation{title: "Read file", body: labeledValue("path", value("path")), specialized: true}, true
	case "fs_list":
		path := value("path")
		if path == "" {
			path = "."
		}
		return toolPresentation{title: "List directory", body: labeledValue("path", path), specialized: true}, true
	case "fs_write":
		body := labeledValue("path", value("path"))
		if content, ok := args["content"].(string); ok {
			body = fields(body, labeledValue("content", fmt.Sprintf("%d bytes", len(content))))
		}
		return toolPresentation{title: "Write file", body: body, specialized: true}, true
	case "fs_edit":
		scope := value("occurrences")
		if scope == "" {
			scope = "first"
		}
		replace, replacePresent := args["replace_string"].(string)
		if replacePresent && replace == "" {
			replace = "(empty)"
		}
		body := fields(
			labeledValue("path", value("path")),
			previewEditValue("search", value("search_string"), max(m.width-16, 20)),
			previewEditValue("replace", replace, max(m.width-16, 20)),
			labeledValue("scope", scope),
		)
		return toolPresentation{title: "Edit file", body: body, specialized: true}, true
	case "fs_mkdir":
		return toolPresentation{title: "Create directory", body: labeledValue("path", value("path")), specialized: true}, true
	case "fs_delete":
		return toolPresentation{title: "Delete", body: labeledValue("path", value("path")), specialized: true}, true
	case "bash":
		p := command("Run command", labeledValue("workdir", value("workdir")), labeledValue("timeout", value("timeout")))
		return p, true
	case "poll_wait":
		p := command("Poll command",
			labeledValue("workdir", value("workdir")),
			labeledValue("interval", value("interval")),
			labeledValue("attempt timeout", value("attempt_timeout")),
			labeledValue("max timeout", value("max_timeout")),
		)
		return p, true
	case "background_task":
		p := command("Start background task", labeledValue("workdir", value("workdir")))
		return p, true
	case "list_tasks":
		status := value("status")
		if status == "" {
			status = "all"
		}
		return toolPresentation{title: "List background tasks", body: labeledValue("status", status), specialized: true}, true
	case "wait_task":
		timeout := value("timeout")
		if timeout == "" {
			timeout = "60s"
		}
		return toolPresentation{title: "Wait for task", body: fields(labeledValue("task", value("task_id")), labeledValue("timeout", timeout)), specialized: true}, true
	case "kill_task":
		return toolPresentation{title: "Stop task", body: labeledValue("task", value("task_id")), specialized: true}, true
	case "explore":
		return toolPresentation{title: "Explore", body: labeledValue("task", value("task")), specialized: true}, true
	case "run_task":
		title := "Delegate task"
		if agent := value("agent_name"); agent != "" {
			title = "Delegate to " + agent
		}
		return toolPresentation{title: title, body: labeledValue("task", value("task")), specialized: true}, true
	case "run_blocking_subagents":
		tasks, _ := args["tasks"].([]any)
		lines := make([]string, 0, len(tasks))
		for i, task := range tasks {
			lines = append(lines, fmt.Sprintf("%d. %s", i+1, terminalSafe(stringValue(task))))
		}
		return toolPresentation{title: fmt.Sprintf("Run %d subagents", len(tasks)), body: strings.Join(lines, "\n"), specialized: true}, true
	default:
		return toolPresentation{}, false
	}
}

func decodeToolArguments(raw string) (map[string]any, bool) {
	if strings.TrimSpace(raw) == "" {
		return map[string]any{}, true
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil || args == nil {
		return map[string]any{}, false
	}
	return args, true
}

func genericToolBody(block *chatBlock, args map[string]any, valid bool) string {
	var sections []string
	if strings.TrimSpace(block.toolArgs) != "" {
		arguments := terminalSafe(block.toolArgs)
		if valid {
			arguments = prettyJSON(args)
		} else if block.pending && !block.toolArgsComplete {
			arguments = mutedStyle.Render("receiving arguments…")
		}
		sections = append(sections, mutedStyle.Render("Arguments")+"\n"+arguments)
	}
	if block.toolOutput != "" {
		sections = append(sections, mutedStyle.Render("Result")+"\n"+terminalSafe(block.toolOutput))
	}
	return strings.Join(sections, "\n\n")
}

func unavailableToolArguments(block *chatBlock) string {
	if block.pending && !block.toolArgsComplete {
		return mutedStyle.Render("receiving arguments…")
	}
	return terminalSafe(block.toolArgs)
}

func toolDetailContent(block *chatBlock) string {
	var sections []string
	if block.toolArgs != "" {
		arguments := terminalSafe(block.toolArgs)
		if args, ok := decodeToolArguments(block.toolArgs); ok {
			arguments = prettyJSON(args)
		}
		sections = append(sections, "Arguments\n"+arguments)
	}
	if block.toolOutput != "" {
		sections = append(sections, "Result\n"+terminalSafe(block.toolOutput))
	}
	if len(sections) == 0 {
		return "No details available."
	}
	return strings.Join(sections, "\n\n")
}

func truncateToolBody(body string, limit int) (string, bool) {
	body = strings.TrimRight(body, "\n")
	if body == "" {
		return "", false
	}
	lines := strings.Split(body, "\n")
	if len(lines) <= limit {
		return body, false
	}
	return strings.Join(lines[:limit], "\n") + fmt.Sprintf("\n… (%d more lines)", len(lines)-limit), true
}

func labeledValue(label, value string) string {
	if value == "" {
		return ""
	}
	return mutedStyle.Render(label+" · ") + terminalSafe(value)
}

func quoteCommand(command string) string {
	if command == "" {
		return ""
	}
	return mutedStyle.Render("$ ") + terminalSafe(command)
}

func previewEditValue(label, value string, width int) string {
	if value == "" {
		return ""
	}
	lines := strings.Split(terminalSafe(value), "\n")
	preview := truncateWidth(lines[0], width)
	if len(lines) > 1 {
		preview += fmt.Sprintf(" · +%d lines", len(lines)-1)
	}
	return labeledValue(label, preview)
}

func nonEmptyStrings(values ...string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}
