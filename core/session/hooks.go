package session

import (
	"context"

	"github.com/basenana/friday/core/providers/openai"
)

// HookHandler is a function that can be registered to run at specific points in the session lifecycle.
// The req parameter allows hooks to access and modify the request before/after LLM calls.
type HookHandler func(ctx context.Context, sess *Session, req openai.Request) error

type Hook interface{}

type BeforeModelHook interface {
	BeforeModel(ctx context.Context, sess *Session, req openai.Request) error
}

type AfterModelHook interface {
	AfterModel(ctx context.Context, sess *Session, req openai.Request, apply *openai.Apply) error
}
