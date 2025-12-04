package agtapi

import (
	"context"

	"github.com/basenana/friday/memory"
	"github.com/basenana/friday/types"
	"github.com/basenana/friday/utils/logger"
	"github.com/google/uuid"
)

const (
	ctxMemoryKey   = "friday.ctx.memory"
	ctxSessionKey  = "friday.ctx.session"
	ctxResponseKey = "friday.ctx.response"
	ctxToolArgsKey = "friday.ctx.tool.args"
)

func MemoryFromContext(ctx context.Context) *memory.Memory {
	m := ctx.Value(ctxMemoryKey)
	if m == nil {
		return nil
	}
	return m.(*memory.Memory)
}

func GetOrCreateSession(ctx context.Context) string {
	s := ctx.Value(ctxSessionKey)
	if s == nil {
		return uuid.New().String()
	}
	return s.(string)
}

func ResponseFromContext(ctx context.Context) *Response {
	r := ctx.Value(ctxResponseKey)
	if r == nil {
		return nil
	}
	return r.(*Response)
}

func OverwriteToolArgsFromContext(ctx context.Context) map[string]string {
	s := ctx.Value(ctxToolArgsKey)
	if s == nil {
		return map[string]string{}
	}
	return s.(map[string]string)
}

type ContextOption func(ctx context.Context) context.Context

func NewContext(ctx context.Context, sessionID string, options ...ContextOption) context.Context {
	// ensure session id
	s := ctx.Value(ctxSessionKey)
	if s == nil || s.(string) != sessionID {
		ctx = context.WithValue(ctx, ctxSessionKey, sessionID)
	}

	for _, opt := range options {
		ctx = opt(ctx)
	}

	return ctx
}

func WithMemory(mem *memory.Memory) ContextOption {
	return func(ctx context.Context) context.Context {
		return context.WithValue(ctx, ctxMemoryKey, mem)
	}
}

func WithOverwriteToolArgs(overwrite map[string]string) ContextOption {
	return func(ctx context.Context) context.Context {
		return context.WithValue(ctx, ctxToolArgsKey, overwrite)
	}
}

func WithResponse(resp *Response) ContextOption {
	return func(ctx context.Context) context.Context {
		return context.WithValue(ctx, ctxResponseKey, resp)
	}
}

func SendEventToResponse(ctx context.Context, evt *types.Event, extraKV ...string) {
	if evt == nil {
		return
	}

	resp := ResponseFromContext(ctx)
	if resp == nil {
		logger.New("eventRecorder").Errorw("no response found in context.Context, event will be dropped", "evt", evt)
		return
	}

	SendEvent(resp, evt, extraKV...)
}
