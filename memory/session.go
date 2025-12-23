package memory

import (
	"context"
	"github.com/basenana/friday/vfs"

	"github.com/basenana/friday/types"
)

type Session interface {
	ID() string
	History(ctx context.Context) []types.Message
	AppendMessage(cte context.Context, ctxID string, message *types.Message)
	RunHooks(ctx context.Context, hookName string, payload *types.SessionPayload) error
	VFS() vfs.VirtualFileSystem
	Session() *types.Session
}

type limitedSession struct {
	id           string
	historyLimit int
}

func newLimitedSession(id string, limited int) Session {
	return &limitedSession{id: id, historyLimit: limited}
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

	if e.historyLimit > 0 && len(payload.History) > e.historyLimit {
		payload.History = payload.History[:e.historyLimit]
	}
	return nil
}

func (e *limitedSession) VFS() vfs.VirtualFileSystem {
	return nil
}

func (e *limitedSession) Session() *types.Session {
	return &types.Session{ID: e.id}
}
