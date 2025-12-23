package session

import (
	"context"

	"github.com/basenana/friday/types"
)

type HookHandler func(ctx context.Context, payload *types.SessionPayload) error

type Hooks interface {
	GetHooks() map[string][]HookHandler
}

func (s *Session) RegisterHooks(hooks Hooks) {
	next := hooks.GetHooks()
	s.mux.Lock()
	defer s.mux.Unlock()
	if s.hooks == nil {
		s.hooks = make(map[string][]HookHandler)
	}

	for hookName, hookFuncs := range next {
		s.hooks[hookName] = append(s.hooks[hookName], hookFuncs...)
	}
}

func (s *Session) RunHooks(ctx context.Context, hookName string, payload *types.SessionPayload) error {
	s.mux.Lock()
	hooks, ok := s.hooks[hookName]
	s.mux.Unlock()

	if !ok || len(hooks) == 0 {
		return nil
	}

	for _, h := range hooks {
		if err := h(ctx, payload); err != nil {
			return err
		}
	}
	return nil
}
