package a2a

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/a2asrv"
	"github.com/a2aproject/a2a-go/a2asrv/eventqueue"
	coreapi "github.com/basenana/friday/core/api"
)

// bridgeResponse reads Friday's streaming response and writes A2A events to the queue.
func bridgeResponse(ctx context.Context, reqCtx *a2asrv.RequestContext, queue eventqueue.Queue, resp *coreapi.Response) error {
	var (
		artifactID a2a.ArtifactID
		textBuf    strings.Builder
	)

	for {
		select {
		case <-ctx.Done():
			return writeTerminalState(ctx, queue, reqCtx, a2a.TaskStateCanceled, nil)

		case err, ok := <-resp.Error():
			if !ok {
				continue
			}
			if err != nil {
				if err == io.EOF {
					return writeTerminalState(ctx, queue, reqCtx, a2a.TaskStateCompleted, finalMessage(textBuf.String()))
				}
				return writeTerminalState(ctx, queue, reqCtx, a2a.TaskStateFailed, errorMessage(err.Error()))
			}

		case delta, ok := <-resp.Deltas():
			if !ok {
				// Delta channel closed, streaming is done
				return writeTerminalState(ctx, queue, reqCtx, a2a.TaskStateCompleted, finalMessage(textBuf.String()))
			}

			if strings.TrimSpace(delta.Content) == "" && textBuf.Len() == 0 {
				continue
			}

			textBuf.WriteString(delta.Content)

			part := a2a.TextPart{Text: delta.Content}
			var event *a2a.TaskArtifactUpdateEvent
			if artifactID == "" {
				event = a2a.NewArtifactEvent(reqCtx, part)
				artifactID = event.Artifact.ID
			} else {
				event = a2a.NewArtifactUpdateEvent(reqCtx, artifactID, part)
			}

			if err := queue.Write(ctx, event); err != nil {
				return fmt.Errorf("failed to write artifact event: %w", err)
			}
		}
	}
}

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
