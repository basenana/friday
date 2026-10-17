package file

import (
	"git.hypo.xyz/agents/friday/core/types"
)

type LoadHistoryHook struct {
	store *FileSessionStore
}

func NewLoadHistoryHook(store *FileSessionStore) *LoadHistoryHook {
	return &LoadHistoryHook{store: store}
}

type PersistHook struct {
	store *FileSessionStore
}

func NewPersistHook(store *FileSessionStore) *PersistHook {
	return &PersistHook{store: store}
}

func (h *PersistHook) Persist(sessionID string, msgs ...types.Message) {
	h.store.AppendMessages(sessionID, msgs...)
}
