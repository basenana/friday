package a2a

import (
	"context"
	"strings"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/a2asrv"
	"github.com/a2aproject/a2a-go/a2asrv/eventqueue"

	"github.com/basenana/friday/actor"
)

// writeTerminalState writes a final status update event.
func writeTerminalState(ctx context.Context, queue eventqueue.Queue, reqCtx *a2asrv.RequestContext, state a2a.TaskState, msg *a2a.Message) error {
	event := a2a.NewStatusUpdateEvent(reqCtx, state, msg)
	event.Final = true
	return queue.Write(ctx, event)
}

// extractTextFromMessage extracts plain text content from an A2A message.
func extractTextFromMessage(msg *a2a.Message) string {
	if msg == nil {
		return ""
	}
	var parts []string
	for _, p := range msg.Parts {
		if tp, ok := p.(a2a.TextPart); ok {
			parts = append(parts, tp.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// errorMessage creates an A2A message with error text.
func errorMessage(text string) *a2a.Message {
	return a2a.NewMessage(a2a.MessageRoleAgent, a2a.TextPart{Text: text})
}

// finalMessage creates an A2A message with the final response text.
func finalMessage(text string) *a2a.Message {
	if text == "" {
		return nil
	}
	return a2a.NewMessage(a2a.MessageRoleAgent, a2a.TextPart{Text: text})
}

func writeTaskTerminalState(ctx context.Context, queue eventqueue.Queue, reqCtx *a2asrv.RequestContext, evt actor.Event, runErr, finalText string) error {
	if runErr != "" {
		return writeTerminalState(ctx, queue, reqCtx, a2a.TaskStateFailed, errorMessage(runErr))
	}

	stopReason, _ := evt.Data["stop_reason"].(string)
	switch stopReason {
	case "cancelled":
		return writeTerminalState(ctx, queue, reqCtx, a2a.TaskStateCanceled, nil)
	case "error":
		return writeTerminalState(ctx, queue, reqCtx, a2a.TaskStateFailed, errorMessage("run failed"))
	default:
		return writeTerminalState(ctx, queue, reqCtx, a2a.TaskStateCompleted, finalMessage(finalText))
	}
}
