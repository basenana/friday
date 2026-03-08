package session

import (
	"context"

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

type AgentRequest interface {
	GetUserMessage() string
	SetUserMessage(msg string)
	GetTools() []*tools.Tool
	AppendTools(...*tools.Tool)
}

type HookPayload struct {
	ModelRequest providers.Request
	ModelApply   *providers.Apply
	AgentRequest AgentRequest
	Messages     []types.Message
}
