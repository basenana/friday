package loop

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
)

type Hook struct{}

var _ session.BeforeAgentHook = (*Hook)(nil)
var _ session.BeforeModelHook = (*Hook)(nil)

func NewHook() *Hook { return &Hook{} }

func (h *Hook) BeforeAgent(ctx context.Context, sess *session.Session, req session.AgentRequest) error {
	state, err := readState(ctx, sess)
	if err != nil || !loopEnabled(state) {
		return err
	}
	isRoot := sess.Root != nil && sess.ID == sess.Root.ID
	req.AppendTools(workingNoteTools(isRoot)...)
	return nil
}

func (h *Hook) BeforeModel(ctx context.Context, sess *session.Session, req providers.Request) error {
	if sess.Root == nil || sess.ID != sess.Root.ID {
		return nil
	}
	state, err := readState(ctx, sess)
	if err != nil || !loopEnabled(state) {
		return err
	}
	req.AppendSystemPrompt(CommonSystemPrompt)
	return nil
}

func workingNoteTools(isRoot bool) []*tools.Tool {
	rootOnly := func(next tools.ToolHandlerFunc) tools.ToolHandlerFunc {
		return func(ctx context.Context, req *tools.Request) (*tools.Result, error) {
			if !isRoot {
				return tools.NewToolResultError("working note tools are only available in the root loop session"), nil
			}
			if req.SessionRecords == nil {
				return tools.NewToolResultError("session records are unavailable"), nil
			}
			return next(ctx, req)
		}
	}

	return []*tools.Tool{
		tools.NewTool("working_note_read",
			tools.WithDescription("Read the complete current Working Note. Use this whenever you need to understand the original request, current progress, decisions, test results, open problems, or what to do next."),
			tools.WithToolHandler(rootOnly(readWorkingNote))),
		tools.NewTool("working_note_append",
			tools.WithDescription("Append useful Markdown to the Working Note without replacing existing content. Use it for newly discovered facts that do not invalidate existing text."),
			tools.WithString("content", tools.Required(), tools.Description("Markdown text to append to the Working Note.")),
			tools.WithToolHandler(rootOnly(appendWorkingNote))),
		tools.NewTool("working_note_edit",
			tools.WithDescription("Edit the Working Note using an exact text replacement. Prefer this when a decision, status, next step, or other existing information has changed."),
			tools.WithString("old_text", tools.Required(), tools.Description("Exact text currently present in the Working Note.")),
			tools.WithString("new_text", tools.Required(), tools.Description("Replacement text. May be empty to delete old text.")),
			tools.WithBoolean("replace_all", tools.Description("Replace every exact occurrence instead of only the first.")),
			tools.WithToolHandler(rootOnly(editWorkingNote))),
		tools.NewTool("working_note_replace",
			tools.WithDescription("Replace the entire Working Note. Use this to reorganize it, remove stale information, and leave a concise, accurate handoff for the next turn."),
			tools.WithString("content", tools.Required(), tools.Description("The complete new Markdown content.")),
			tools.WithToolHandler(rootOnly(replaceWorkingNote))),
		tools.NewTool("finish_loop",
			tools.WithDescription("Finish the autonomous Loop after the complete original request has been satisfied, there is genuinely no useful work left, or continuing would be unsafe or inappropriate. This does not end the current turn; update the Working Note if needed and provide the final user-facing summary afterward."),
			tools.WithToolHandler(rootOnly(finishLoop))),
	}
}

func readWorkingNote(ctx context.Context, req *tools.Request) (*tools.Result, error) {
	raw, err := req.SessionRecords.ReadRecord(ctx, WorkingNoteNamespace)
	if errors.Is(err, session.ErrRecordNotFound) {
		return tools.NewToolResultText(""), nil
	}
	if err != nil {
		return tools.NewToolResultError("read working note: " + err.Error()), nil
	}
	return tools.NewToolResultText(string(raw)), nil
}

func appendWorkingNote(ctx context.Context, req *tools.Request) (*tools.Result, error) {
	content, _ := req.Arguments["content"].(string)
	if content == "" {
		return tools.NewToolResultError("content must not be empty"), nil
	}
	err := req.SessionRecords.UpdateRecord(ctx, WorkingNoteNamespace, func(current []byte) ([]byte, error) {
		if len(current) == 0 {
			return []byte(content), nil
		}
		return []byte(strings.TrimRight(string(current), "\n") + "\n\n" + content), nil
	})
	return mutationResult("append working note", err)
}

func editWorkingNote(ctx context.Context, req *tools.Request) (*tools.Result, error) {
	oldText, _ := req.Arguments["old_text"].(string)
	newText, _ := req.Arguments["new_text"].(string)
	replaceAll, _ := req.Arguments["replace_all"].(bool)
	if oldText == "" {
		return tools.NewToolResultError("old_text must not be empty"), nil
	}
	err := req.SessionRecords.UpdateRecord(ctx, WorkingNoteNamespace, func(current []byte) ([]byte, error) {
		text := string(current)
		if !strings.Contains(text, oldText) {
			return nil, fmt.Errorf("old_text was not found; call working_note_read and retry with exact current text")
		}
		count := 1
		if replaceAll {
			count = -1
		}
		return []byte(strings.Replace(text, oldText, newText, count)), nil
	})
	return mutationResult("edit working note", err)
}

func replaceWorkingNote(ctx context.Context, req *tools.Request) (*tools.Result, error) {
	content, _ := req.Arguments["content"].(string)
	err := req.SessionRecords.UpdateRecord(ctx, WorkingNoteNamespace, func([]byte) ([]byte, error) {
		return []byte(content), nil
	})
	return mutationResult("replace working note", err)
}

func finishLoop(ctx context.Context, req *tools.Request) (*tools.Result, error) {
	err := req.SessionRecords.UpdateRecord(ctx, StateNamespace, func(current []byte) ([]byte, error) {
		switch State(strings.TrimSpace(string(current))) {
		case StateActive:
			return []byte(StateCompleted), nil
		case StateCancelled:
			return nil, errors.New("the loop was cancelled by the user")
		default:
			return nil, errors.New("there is no active loop to finish")
		}
	})
	if err != nil {
		return tools.NewToolResultError("finish loop: " + err.Error()), nil
	}
	return tools.NewToolResultText("Loop is complete. Update the Working Note if needed and provide the final user-facing summary now."), nil
}

func mutationResult(action string, err error) (*tools.Result, error) {
	if err != nil {
		return tools.NewToolResultError(action + ": " + err.Error()), nil
	}
	return tools.NewToolResultText("Working Note updated."), nil
}
