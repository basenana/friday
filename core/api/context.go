package api

import (
	"context"

	"github.com/basenana/friday/core/session"
)

const (
	ctxSessionKey  = "friday.ctx.session"
	ctxResponseKey = "friday.ctx.response"
	ctxToolArgsKey = "friday.ctx.tool.args"
	ctxEventBusKey = "friday.ctx.eventbus"
)

func SessionFromContext(ctx context.Context) *session.Session {
	s := ctx.Value(ctxSessionKey)
	if s == nil {
		return nil
	}
	return s.(*session.Session)
}

func OverwriteToolArgsFromContext(ctx context.Context) map[string]string {
	s := ctx.Value(ctxToolArgsKey)
	if s == nil {
		return map[string]string{}
	}
	return s.(map[string]string)
}

type ContextOption func(ctx context.Context) context.Context

func NewContext(ctx context.Context, sess *session.Session, options ...ContextOption) context.Context {
	s := ctx.Value(ctxSessionKey)
	if s == nil || s.(*session.Session).ID != sess.ID {
		ctx = context.WithValue(ctx, ctxSessionKey, sess)
	}

	for _, opt := range options {
		ctx = opt(ctx)
	}

	return ctx
}

func WithResponse(resp *Response) ContextOption {
	return func(ctx context.Context) context.Context {
		return context.WithValue(ctx, ctxResponseKey, resp)
	}
}
