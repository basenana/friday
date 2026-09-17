package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/basenana/friday/core/collaboration"
	"github.com/charmbracelet/x/ansi"
)

const toolCardLineLimit = 10
const toolErrorLineLimit = 3

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
	failed := !block.pending && !block.success && !block.interrupted
	bodyWidth := max(m.width-6, 10)
	wrappedBody := ansi.Wrap(presentation.body, bodyWidth, "")
	body, truncated := truncateToolBody(wrappedBody, toolCardLineLimit)
	if failed {
		wrappedError := ansi.Wrap(toolErrorText(block.toolOutput), bodyWidth, "")
		errorBody, errorTruncated := truncateToolBody(wrappedError, toolErrorLineLimit)
		errorBody = mutedStyle.Render("Error") + "\n" + errorBody
		if body == "" {
			body = errorBody
		} else {
			body += "\n" + errorBody
		}
		truncated = truncated || errorTruncated
	}

	lines := []string{header}
	if body != "" {
		lines = append(lines, body)
	}
	if block.timedOut {
		detail := "timed out"
		if block.timeoutKind != "" {
			detail += " · " + strings.ReplaceAll(block.timeoutKind, "_", " ")
		}
		lines = append(lines, mutedStyle.Render("↳ "+detail))
	} else if block.interrupted {
		lines = append(lines, mutedStyle.Render("↳ interrupted"))
	} else if block.id != "" && truncated {
		lines = append(lines, mutedStyle.Render("/show "+shortID(block.id)+" · full details"))
	}
	return style.Render(strings.Join(lines, "\n"))
}

func toolErrorText(output string) string {
	text := strings.TrimSpace(terminalSafe(output))
	if len(text) >= len("Error:") && strings.EqualFold(text[:len("Error:")], "Error:") {
		text = strings.TrimSpace(text[len("Error:"):])
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError    bool `json:"is_error"`
		MCPIsError bool `json:"isError"`
	}
	if json.Unmarshal([]byte(text), &result) == nil && (result.IsError || result.MCPIsError) {
		var parts []string
		for _, content := range result.Content {
			if content.Type == "text" && strings.TrimSpace(content.Text) != "" {
				parts = append(parts, strings.TrimSpace(content.Text))
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}
	if text == "" {
		return "Tool failed without an error message."
	}
	return text
}

func toolStatusStyle(block *chatBlock) (style lipgloss.Style, icon string) {
	base := toolBoxStyle.Copy()
	switch {
	case block.timedOut:
		return base.BorderForeground(themeError), "◷"
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
	if presentation, ok := m.presentBuiltinTool(block, args); ok {
		if !valid && strings.TrimSpace(block.toolArgs) != "" && block.toolName != "write_todos" && block.toolName != "request_user_input" && block.toolName != collaboration.SubmitPlanToolName && block.toolName != "load_skill" {
			presentation.body = unavailableToolArguments(block)
		}
		return presentation
	}
	return toolPresentation{
		title: block.toolName,
		body:  genericToolBody(block, args, valid),
	}
}

func (m *model) presentBuiltinTool(block *chatBlock, args map[string]any) (toolPresentation, bool) {
	value := func(key string) string { return stringValue(args[key]) }
	fields := func(items ...string) string { return strings.Join(nonEmptyStrings(items...), "\n") }
	command := func(title string, extras ...string) toolPresentation {
		items := append([]string{quoteCommand(value("command"))}, extras...)
		body := fields(items...)
		return toolPresentation{title: title, body: body, specialized: true}
	}

	switch block.toolName {
	case "write_todos":
		return toolPresentation{title: "Update todos", specialized: true}, true
	case "request_user_input":
		return toolPresentation{title: "Ask user", specialized: true}, true
	case collaboration.SubmitPlanToolName:
		return toolPresentation{title: "Submit plan", specialized: true}, true
	case "load_skill":
		name := value("name")
		description := ""
		if result, ok := decodeLoadSkillResult(block.toolOutput); ok {
			if result.Name != "" {
				name = result.Name
			}
			description = result.Description
		}
		return toolPresentation{
			title: "Load skill",
			body: fields(
				labeledValue("name", name),
				labeledValue("description", description),
			),
			specialized: true,
		}, true
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
		tasks, _ := args["tasks"].([]any)
		lines := make([]string, 0, len(tasks))
		for i, task := range tasks {
			lines = append(lines, fmt.Sprintf("%d. %s", i+1, terminalSafe(stringValue(task))))
		}
		return toolPresentation{title: taskCountTitle("Explore", len(tasks)), body: strings.Join(lines, "\n"), specialized: true}, true
	case "run_task":
		tasks, _ := args["tasks"].([]any)
		lines := make([]string, 0, len(tasks))
		for i, raw := range tasks {
			item, _ := raw.(map[string]any)
			agent := strings.TrimSpace(stringValue(item["agent_name"]))
			task := strings.TrimSpace(stringValue(item["task"]))
			if agent != "" {
				task = agent + " · " + task
			}
			lines = append(lines, fmt.Sprintf("%d. %s", i+1, terminalSafe(task)))
		}
		return toolPresentation{title: taskCountTitle("Delegate", len(tasks)), body: strings.Join(lines, "\n"), specialized: true}, true
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

func taskCountTitle(action string, count int) string {
	noun := "tasks"
	if count == 1 {
		noun = "task"
	}
	return fmt.Sprintf("%s %d %s", action, count, noun)
}

type loadSkillResult struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func decodeLoadSkillResult(raw string) (loadSkillResult, bool) {
	var result loadSkillResult
	if strings.TrimSpace(raw) == "" || json.Unmarshal([]byte(raw), &result) != nil {
		return loadSkillResult{}, false
	}
	return result, true
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
	failed := !block.pending && !block.success && !block.interrupted
	if block.toolOutput != "" && !failed {
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
