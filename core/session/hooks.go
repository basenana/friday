package session

import (
	"context"
	"time"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
)

// HookHandler is a function that can be registered to run at specific points in the session lifecycle.
// The req parameter allows hooks to access and modify the request before/after LLM calls.
type HookHandler func(ctx context.Context, sess *Session, req providers.Request) error

type Hook interface{}

type BeforeAgentHook interface {
	BeforeAgent(ctx context.Context, sess *Session, req AgentRequest) error
}

type BeforeModelHook interface {
	BeforeModel(ctx context.Context, sess *Session, req providers.Request) error
}

type AfterModelHook interface {
	AfterModel(ctx context.Context, sess *Session, req providers.Request, apply *providers.Apply) error
}

// ModelCallStats carries per-LLM-call observability data: token usage,
// latency, model name, outcome (including failures) and the assembled
// response text. Unlike AfterModelHook, AfterModelCallHook fires on both
// success and error paths of the model call.
type ModelCallStats struct {
	Model      string
	Tokens     providers.Tokens
	StartAt    time.Time
	DurationMs int64
	// Err is empty on success; on failure it carries the error message.
	Err       string
	Content   string
	Reasoning string
	ToolCalls []providers.ToolCall
	// RawRequest is the exact wire-format request body sent to the provider,
	// captured via SDK middleware; empty when no capture was attached.
	RawRequest string
}

// AfterModelCallHook fires after every LLM call with per-call stats, on both
// the success and error paths (including context cancellation).
// Note the asymmetry with the other hooks: RunHooks skips AfterModelCall
// entirely when HookPayload.ModelCallStats is nil.
type AfterModelCallHook interface {
	AfterModelCall(ctx context.Context, sess *Session, req providers.Request, stats *ModelCallStats) error
}

type AfterToolHook interface {
	AfterTool(ctx context.Context, sess *Session, payload ToolPayload) error
}

type ToolExecution struct {
	Call     providers.ToolCall
	Messages []types.Message
}

type AgentRequest interface {
	GetUserMessage() string
	SetUserMessage(msg string)
	GetTools() []*tools.Tool
	AppendTools(...*tools.Tool)
}

type HookPayload struct {
	ModelRequest   providers.Request
	ModelApply     *providers.Apply
	ModelCallStats *ModelCallStats
	AgentRequest   AgentRequest
	Messages       []types.Message
	Executions     []ToolExecution
	ToolCalls      []providers.ToolCall
}

type ToolPayload struct {
	Executions []ToolExecution
}
