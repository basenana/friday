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
			tools.WithDescription(`Complete the autonomous Loop.

Call this tool only during the final update phase, after rereading the complete Working Note and auditing it against the actual repository state and current verification results.

Before calling, confirm that the complete original request and every task, plan, selected task, checklist item, follow-up, next step, and unresolved problem recorded in the Working Note have all been completed and verified. There must be no unfinished, uncertain, unverified, useful, or actionable work remaining.

Completing only the current selected task, one checklist item, or one planned slice is never sufficient. Do not call this tool during bootstrap, task selection, development, review, or recovery. If work was completed during one of those phases, update the Working Note with its completion status and verification evidence, then end the turn normally.

If anything remains to be done, do not call this tool. Keep the remaining work explicit in the Working Note so the Loop can continue.`),
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
	currentPhase, err := readPhase(ctx, req.SessionRecords)
	if err != nil {
		return tools.NewToolResultError("finish loop: read current loop phase: " + err.Error()), nil
	}
	if currentPhase != phaseUpdate {
		return tools.NewToolResultText(fmt.Sprintf(`The Loop cannot finish during the current %s phase. finish_loop may only complete the Loop during the final update phase.

Do not retry finish_loop in this turn. If you completed any work, update the Working Note now with its completion status and verification evidence, then end the turn normally. The Loop will continue through the remaining work and eventually reach the update phase.`, currentPhase)), nil
	}

	state, err := readState(ctx, req.SessionRecords)
	if err == nil {
		switch state {
		case StateActive, StateSuspended:
			var changed bool
			changed, err = transitionState(ctx, req.SessionRecords, []State{StateActive, StateSuspended}, StateCompleted)
			if err == nil && !changed {
				state, err = readState(ctx, req.SessionRecords)
				if err == nil && state != StateCompleted {
					if state == StateCancelled {
						err = errors.New("the loop was cancelled by the user")
					} else {
						err = errors.New("there is no active loop to finish")
					}
				}
			}
		case StateCancelled:
			err = errors.New("the loop was cancelled by the user")
		default:
			err = errors.New("there is no active loop to finish")
		}
	}
	if err != nil {
		return tools.NewToolResultError("finish loop: " + err.Error()), nil
	}
	return tools.NewToolResultText("The Loop is complete. The complete original request and every task, plan, checklist item, follow-up, next step, and unresolved problem recorded in the Working Note have been completed and verified, and no actionable work remains. Provide the final user-facing summary now."), nil
}

func mutationResult(action string, err error) (*tools.Result, error) {
	if err != nil {
		return tools.NewToolResultError(action + ": " + err.Error()), nil
	}
	return tools.NewToolResultText("Working Note updated."), nil
}
