package memory

import (
	"context"

	"github.com/basenana/friday/tools"
	"github.com/basenana/friday/types"
)

type Session interface {
	ID() string
	History(ctx context.Context) []types.Message
	AppendMessage(cte context.Context, ctxID string, message *types.Message)
	RunHooks(ctx context.Context, hookName string, payload *types.SessionPayload) error
	Tools() []*tools.Tool
}

type limitedSession struct {
	id           string
	historyLimit int
}

func newLimitedSession(id string) Session {
	return &limitedSession{id: id, historyLimit: 20}
}

func (e *limitedSession) ID() string {
	return e.id
}

func (e *limitedSession) History(ctx context.Context) []types.Message {
	return make([]types.Message, 0, 4)
}

func (e *limitedSession) AppendMessage(ctx context.Context, ctxID string, message *types.Message) {
	return
}

func (e *limitedSession) RunHooks(ctx context.Context, hookName string, payload *types.SessionPayload) error {
	if hookName != types.SessionHookBeforeModel {
		return nil
	}

	if len(payload.History) > e.historyLimit {
		payload.History = payload.History[:e.historyLimit]
	}
	return nil
}

func (e *limitedSession) Tools() []*tools.Tool {
	return []*tools.Tool{}
}
