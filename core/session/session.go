package session

import (
	"context"
	"sync"
	"time"

	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/state"
	"github.com/basenana/friday/core/types"
)

// MessageWriter defines the interface for persisting messages
type MessageWriter interface {
	AppendMessages(sessionID string, msgs ...types.Message) error
	ReplaceMessages(sessionID string, msgs ...types.Message) error
}

type Session struct {
	ID        string
	Root      *Session
	Parent    *Session
	Children  []*Session
	History   []types.Message
	State     state.State
	CreatedAt time.Time
	Temporary bool

	compactThreshold int64

	hooks  []Hook
	llm    providers.Client
	writer MessageWriter // for auto-persisting messages
	mu     sync.RWMutex
}

func New(id string, llm providers.Client, options ...Option) *Session {
	s := &Session{
		ID:               id,
		History:          make([]types.Message, 0, 10),
		compactThreshold: CompactThreshold,
		hooks:            make([]Hook, 0),
		CreatedAt:        time.Now(),
		llm:              llm,
	}
	s.Root = s

	for _, option := range options {
		option(s)
	}

	if s.State == nil {
		s.State = state.NewInMemory()
	}
	return s
}

func (s *Session) Fork() *Session {
	s.mu.Lock()
	fork := &Session{
		ID:               types.NewID(),
		Root:             s.Root,
		Parent:           s,
		History:          make([]types.Message, len(s.History)),
		State:            s.State,
		CreatedAt:        time.Now(),
		Temporary:        s.Temporary,
		compactThreshold: s.compactThreshold,
		hooks:            s.hooks,
		llm:              s.llm,
	}
	copy(fork.History, s.History)
	s.Children = append(s.Children, fork)
	s.mu.Unlock()

	return fork
}

func (s *Session) AppendMessage(msgList ...*types.Message) {
	var toPersist []types.Message

	s.mu.Lock()
	for _, msg := range msgList {
		if msg.Time.IsZero() {
			msg.Time = time.Now()
		}
		s.History = append(s.History, *msg)
		toPersist = append(toPersist, *msg)
	}
	s.mu.Unlock()

	// Persist messages outside the lock to avoid potential deadlocks
	// Skip persistence for temporary sessions
	if !s.Temporary && s.writer != nil && len(toPersist) > 0 {
		s.writer.AppendMessages(s.ID, toPersist...)
	}
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

func (s *Session) CleanHooks() {
	s.hooks = nil
}

func (s *Session) RunHooks(ctx context.Context, hookType types.SessionType, payload HookPayload) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, h := range s.hooks {
		switch hookType {
		case types.SessionHookBeforeAgent:
			if bh, ok := h.(BeforeAgentHook); ok {
				if err := bh.BeforeAgent(ctx, s, payload.AgentRequest); err != nil {
					return err
				}
			}
		case types.SessionHookBeforeModel:
			if bh, ok := h.(BeforeModelHook); ok {
				if err := bh.BeforeModel(ctx, s, payload.ModelRequest); err != nil {
					return err
				}
			}
		case types.SessionHookAfterModel:
			if ah, ok := h.(AfterModelHook); ok {
				if err := ah.AfterModel(ctx, s, payload.ModelRequest, payload.ModelApply); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

type Option func(*Session)

func WithHistory(messages ...types.Message) Option {
	return func(s *Session) {
		s.History = messages
	}
}

func WithHooks(hooks ...Hook) Option {
	return func(s *Session) {
		s.hooks = append(s.hooks, hooks...)
	}
}

func WithState(st state.State) Option {
	return func(s *Session) {
		s.State = st
	}
}

func WithCompactThreshold(ct int64) Option {
	return func(s *Session) {
		s.compactThreshold = ct
	}
}

func WithMessageWriter(w MessageWriter) Option {
	return func(s *Session) {
		s.writer = w
	}
}

func WithTemporary(v bool) Option {
	return func(s *Session) {
		s.Temporary = v
	}
}

func (s *Session) ReplaceHistory(msgs ...types.Message) error {
	s.mu.Lock()
	s.History = msgs
	s.mu.Unlock()

	// Skip persistence for temporary sessions
	if s.Temporary {
		return nil
	}
	if s.writer != nil {
		return s.writer.ReplaceMessages(s.ID, msgs...)
	}
	return nil
}
