package openairesponse

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/providers/common"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
	"github.com/openai/openai-go/responses"
)

type pendingToolCall struct {
	itemID    string
	callID    string
	name      string
	arguments string
}

type response struct {
	*providers.CommonResponse
	request providers.Request
	logger  logger.Logger

	accumulatedContent string
	reasoningEmitted   bool
	emitted            bool
	failed             bool
	pendingTools       map[string]*pendingToolCall
	toolOrder          []string
	emittedTools       map[string]struct{}
	emittedReasoning   map[string]struct{}
}

func newResponse(request providers.Request) *response {
	return &response{
		CommonResponse:   providers.NewCommonResponse(),
		request:          request,
		pendingTools:     make(map[string]*pendingToolCall),
		emittedTools:     make(map[string]struct{}),
		emittedReasoning: make(map[string]struct{}),
	}
}

func (r *response) nextEvent(event responses.ResponseStreamEventUnion) error {
	switch event.Type {
	case "response.output_text.delta":
		delta := event.AsResponseOutputTextDelta().Delta
		if delta != "" {
			r.accumulatedContent += delta
			r.emit(providers.Delta{Content: delta})
		}
	case "response.refusal.delta":
		delta := event.AsResponseRefusalDelta().Delta
		if delta != "" {
			r.accumulatedContent += delta
			r.emit(providers.Delta{Content: delta})
		}
	case "response.reasoning_summary_text.delta":
		delta := event.AsResponseReasoningSummaryTextDelta().Delta
		if delta != "" {
			r.reasoningEmitted = true
			r.emit(providers.Delta{Reasoning: delta})
		}
	case "response.reasoning_summary.delta":
		delta := event.Delta.OfString
		if delta != "" {
			r.reasoningEmitted = true
			r.emit(providers.Delta{Reasoning: delta})
		}
	case "response.output_item.added":
		r.rememberTool(event.AsResponseOutputItemAdded().Item)
	case "response.function_call_arguments.delta":
		e := event.AsResponseFunctionCallArgumentsDelta()
		tool := r.ensurePendingTool(e.ItemID)
		tool.arguments += e.Delta
	case "response.function_call_arguments.done":
		e := event.AsResponseFunctionCallArgumentsDone()
		tool := r.ensurePendingTool(e.ItemID)
		tool.arguments = e.Arguments
	case "response.output_item.done":
		r.emitOutputItem(event.AsResponseOutputItemDone().Item)
	case "response.completed":
		completed := event.AsResponseCompleted().Response
		r.updateUsage(completed.Usage)
		r.emitCompletedFallback(completed)
	case "response.failed":
		failed := event.AsResponseFailed().Response
		r.updateUsage(failed.Usage)
		if failed.Error.Message != "" {
			return fmt.Errorf("responses API error %s: %s", failed.Error.Code, failed.Error.Message)
		}
		return fmt.Errorf("responses API request failed")
	case "response.incomplete":
		incomplete := event.AsResponseIncomplete().Response
		r.updateUsage(incomplete.Usage)
		r.emitCompletedFallback(incomplete)
	case "error":
		e := event.AsError()
		return fmt.Errorf("responses API error %s: %s", e.Code, e.Message)
	}
	return nil
}

func (r *response) rememberTool(item responses.ResponseOutputItemUnion) {
	if item.Type != "function_call" {
		return
	}
	tool := r.ensurePendingTool(item.ID)
	tool.callID = item.CallID
	tool.name = item.Name
	if item.Arguments != "" {
		tool.arguments = item.Arguments
	}
}

func (r *response) ensurePendingTool(itemID string) *pendingToolCall {
	if tool, ok := r.pendingTools[itemID]; ok {
		return tool
	}
	tool := &pendingToolCall{itemID: itemID}
	r.pendingTools[itemID] = tool
	r.toolOrder = append(r.toolOrder, itemID)
	return tool
}

func (r *response) emitOutputItem(item responses.ResponseOutputItemUnion) {
	switch item.Type {
	case "function_call":
		key := item.CallID
		if key == "" {
			key = item.ID
		}
		if _, ok := r.emittedTools[key]; ok {
			return
		}
		arguments, toolErr, ok := common.NormalizeToolUseArguments(item.Arguments, item.Name)
		if !ok && r.logger != nil {
			r.logger.Warnw("non-object tool arguments emitted", "tool", item.Name)
		}
		r.emit(providers.Delta{ToolUse: []providers.ToolCall{{
			ID: key, Name: item.Name, Arguments: arguments, Error: toolErr,
		}}})
		r.emittedTools[key] = struct{}{}
		delete(r.pendingTools, item.ID)
	case "reasoning":
		raw := item.RawJSON()
		if raw == "" {
			encoded, _ := json.Marshal(item.AsReasoning())
			raw = string(encoded)
		}
		reasoningKey := item.ID
		if reasoningKey == "" {
			reasoningKey = raw
		}
		if raw != "" {
			if _, ok := r.emittedReasoning[reasoningKey]; !ok {
				r.emit(providers.Delta{ReasoningSignature: raw})
				r.emittedReasoning[reasoningKey] = struct{}{}
			}
		}
		if !r.reasoningEmitted {
			for _, summary := range item.Summary {
				if summary.Text != "" {
					r.emit(providers.Delta{Reasoning: summary.Text})
					r.reasoningEmitted = true
				}
			}
		}
	}
}

func (r *response) emitCompletedFallback(completed responses.Response) {
	if r.accumulatedContent == "" {
		if text := responseText(&completed); text != "" {
			r.accumulatedContent = text
			r.emit(providers.Delta{Content: text})
		}
	}
	for _, item := range completed.Output {
		r.emitOutputItem(item)
	}
}

func responseText(response *responses.Response) string {
	if response == nil {
		return ""
	}
	var text strings.Builder
	for _, item := range response.Output {
		for _, content := range item.Content {
			switch content.Type {
			case "output_text":
				text.WriteString(content.Text)
			case "refusal":
				text.WriteString(content.Refusal)
			}
		}
	}
	return text.String()
}

func (r *response) flushPendingTools() {
	for _, itemID := range r.toolOrder {
		tool, ok := r.pendingTools[itemID]
		if !ok || tool.name == "" {
			continue
		}
		key := tool.callID
		if key == "" {
			key = tool.itemID
		}
		if _, ok := r.emittedTools[key]; ok {
			continue
		}
		arguments, toolErr, _ := common.NormalizeToolUseArguments(tool.arguments, tool.name)
		r.emit(providers.Delta{ToolUse: []providers.ToolCall{{
			ID: key, Name: tool.name, Arguments: arguments, Error: toolErr,
		}}})
		r.emittedTools[key] = struct{}{}
	}
}

func (r *response) updateUsage(usage responses.ResponseUsage) {
	r.SetTokens(providers.Tokens{
		PromptTokens:       usage.InputTokens,
		CompletionTokens:   usage.OutputTokens,
		CachedPromptTokens: usage.InputTokensDetails.CachedTokens,
		TotalTokens:        usage.TotalTokens,
	})
}

func (r *response) applyTokenFallback(messages []types.Message) {
	tokens := r.Tokens()
	overhead := session.EstimateRequestOverhead(r.request)
	tokens.PromptTokens, tokens.CompletionTokens, tokens.TotalTokens = common.ApplyTokenFallback(
		tokens.PromptTokens,
		tokens.CompletionTokens,
		r.accumulatedContent,
		messages,
		overhead,
	)
	r.SetTokens(tokens)
}

func (r *response) emit(delta providers.Delta) {
	r.emitted = true
	r.Stream <- delta
}

func (r *response) canRetry() bool { return !r.emitted }

func (r *response) resetForRetry() {
	r.SetTokens(providers.Tokens{})
	r.accumulatedContent = ""
	r.reasoningEmitted = false
	r.emitted = false
	r.failed = false
	r.pendingTools = make(map[string]*pendingToolCall)
	r.toolOrder = nil
	r.emittedTools = make(map[string]struct{})
	r.emittedReasoning = make(map[string]struct{})
}

func (r *response) fail(err error) {
	r.failed = true
	r.Err <- err
}

func (r *response) close() {
	if !r.failed {
		r.flushPendingTools()
	}
	close(r.Stream)
	close(r.Err)
}

var _ providers.Response = (*response)(nil)
