package setup

import (
	"context"

	"github.com/basenana/friday/core/providers"
	coreSession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/workspace"
)

// memoryHook injects fresh workspace memory content into each model request
// at composition time. It deliberately does not mutate the session history:
// memory messages used to be prepended to History once and then persisted
// forever, so long-lived sessions never saw updated memory. Following the
// pattern of the planning/contextmgr hooks, the messages are prepended to the
// per-request history instead, and re-read from disk on every model call.
type memoryHook struct {
	workspace *workspace.Workspace
}

var _ coreSession.BeforeModelHook = (*memoryHook)(nil)

func newMemoryHook(ws *workspace.Workspace) *memoryHook {
	return &memoryHook{workspace: ws}
}

func (h *memoryHook) BeforeModel(ctx context.Context, sess *coreSession.Session, req providers.Request) error {
	loaded, err := h.workspace.Load()
	if err != nil {
		// Memory is best-effort context; never block the model call.
		return nil
	}
	if len(loaded.MemoryHistory) == 0 {
		return nil
	}

	history := make([]types.Message, 0, len(loaded.MemoryHistory)+len(req.History()))
	history = append(history, loaded.MemoryHistory...)
	history = append(history, req.History()...)
	req.SetHistory(history)
	return nil
}
