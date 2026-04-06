package setup

import (
	"context"

	"github.com/basenana/friday/core/contextmgr"
	coresession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
)

type sessionMemoryBridge struct {
	snapshot []types.Message
}

var _ contextmgr.MemoryBridge = (*sessionMemoryBridge)(nil)

func newSessionMemoryBridge(snapshot []types.Message) *sessionMemoryBridge {
	return &sessionMemoryBridge{
		snapshot: append([]types.Message(nil), snapshot...),
	}
}

func (b *sessionMemoryBridge) LoadSessionMemory(_ context.Context, _ *coresession.Session) ([]types.Message, error) {
	return append([]types.Message(nil), b.snapshot...), nil
}
