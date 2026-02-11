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

	hooks map[string][]HookHandler
	llm   openai.Client
	mu    sync.RWMutex
}

func New(id string, llm openai.Client) *Session {
	s := &Session{
		ID:        id,
		History:   make([]types.Message, 0, 10),
		hooks:     make(map[string][]HookHandler),
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

func (s *Session) RegisterHook(name string, handler HookHandler) {
	if handler == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hooks[name] = append(s.hooks[name], handler)
}

func (s *Session) RunHooks(ctx context.Context, hookName string, req openai.Request) error {
	s.mu.RLock()
	hooks, ok := s.hooks[hookName]
	s.mu.RUnlock()
	if !ok || len(hooks) == 0 {
		return nil
	}

	if hookName == types.SessionHookBeforeModel {
		return s.checkAndCompactHistory(ctx, req)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, h := range hooks {
		if err := h(ctx, s, req); err != nil {
			return err
		}
	}

	return nil
}
