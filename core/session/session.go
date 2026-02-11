package session

import (
	"context"
	"sync"
	"time"

	"github.com/basenana/friday/core/providers/openai"
	"github.com/basenana/friday/core/types"
)

type Session struct {
	ID        string
	Root      *Session
	Parent    *Session
	Children  []*Session
	History   []types.Message
	CreatedAt time.Time

	hooks []Hook
	llm   openai.Client
	mu    sync.RWMutex
}

func New(id string, llm openai.Client) *Session {
	s := &Session{
		ID:        id,
		History:   make([]types.Message, 0, 10),
		hooks:     make([]Hook, 0),
		CreatedAt: time.Now(),
		llm:       llm,
	}
	s.Root = s
	return s
}

func (s *Session) Fork() *Session {
	s.mu.Lock()
	fork := &Session{
		ID:        types.NewID(),
		Root:      s.Root,
		Parent:    s,
		History:   make([]types.Message, len(s.History)),
		hooks:     s.hooks,
		CreatedAt: time.Now(),
		llm:       s.llm,
	}
	copy(fork.History, s.History)
	s.Children = append(s.Children, fork)
	s.mu.Unlock()

	return fork
}

func (s *Session) AppendMessage(msg *types.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.History = append(s.History, *msg)
}

func (s *Session) Tokens() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var total int64
	for _, msg := range s.History {
		total += msg.FuzzyTokens()
	}
	return total
}

func (s *Session) RegisterHook(handler Hook) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hooks = append(s.hooks, handler)
}

func (s *Session) RunHooks(ctx context.Context, hookName string, req openai.Request, apply *openai.Apply) error {
	if len(s.hooks) == 0 {
		return nil
	}

	switch hookName {
	case types.SessionHookBeforeModel:
		if err := s.checkAndCompactHistory(ctx, req); err != nil {
			return err
		}

		for _, hook := range s.hooks {
			handler, ok := hook.(BeforeModelHook)
			if !ok {
				continue
			}
			if err := handler.BeforeModel(ctx, s, req); err != nil {
				return err
			}
		}

	case types.SessionHookAfterModel:

		for _, hook := range s.hooks {
			handler, ok := hook.(AfterModelHook)
			if !ok {
				continue
			}
			if err := handler.AfterModel(ctx, s, req, apply); err != nil {
				return err
			}
		}
	}

	return nil
}
