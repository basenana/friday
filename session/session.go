package session

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/basenana/friday/storehouse"
	"github.com/basenana/friday/tools"
	"github.com/basenana/friday/types"
	"github.com/basenana/friday/utils/logger"
	"go.uber.org/zap"
)

type Manager struct {
	store  storehouse.Storehouse
	logger *zap.SugaredLogger
}

func (m *Manager) ListSessions(ctx context.Context, filter map[string]string) ([]*types.Session, error) {
	return m.store.ListSessions(ctx, filter, false)
}

func (m *Manager) NewChatSession(ctx context.Context, metadata map[string]string, systemPrompt string) (*types.Session, error) {
	session := &types.Session{
		Type:     types.SessionTypeChat,
		Metadata: metadata,
		System:   systemPrompt,
	}
	return m.store.CreateSession(ctx, session)
}

func (m *Manager) NewAgenticSession(ctx context.Context, metadata map[string]string, systemPrompt string) (*types.Session, error) {
	session := &types.Session{
		Type:     types.SessionTypeAgentic,
		Metadata: metadata,
		System:   systemPrompt,
	}
	return m.store.CreateSession(ctx, session)
}

func (m *Manager) OpenSession(ctx context.Context, sessionID string) (*Session, error) {
	s, err := m.store.OpenSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return NewSession(s, m.store), nil
}

func (m *Manager) DeleteSession(ctx context.Context, sessionID string) error {
	return m.store.CloseSession(ctx, sessionID)
}

func NewManager(store storehouse.Storehouse) *Manager {
	return &Manager{
		store:  store,
		logger: logger.New("session.manager"),
	}
}

type Session struct {
	session  *types.Session
	store    storehouse.Storehouse
	hooks    map[string][]HookHandler
	notebook Notebook
	mux      sync.Mutex
	logger   *zap.SugaredLogger
}

func NewSession(s *types.Session, store storehouse.Storehouse) *Session {
	return &Session{
		session:  s,
		store:    store,
		hooks:    make(map[string][]HookHandler),
		notebook: NewInMemoryNotebook(),
		logger:   logger.New("session").With(zap.String("id", s.ID)),
	}
}

func (s *Session) ID() string {
	return s.session.ID
}

func (s *Session) History(ctx context.Context) []types.Message {
	return s.contextHistory(ctx, s.ID())
}

func (s *Session) contextHistory(ctx context.Context, contextID string) []types.Message {
	allMessages, err := s.store.ListMessages(ctx, contextID)
	if err != nil {
		s.logger.Errorw("failed to list session messages", zap.Error(err))
		return nil
	}

	var result []types.Message
	for _, msg := range allMessages {
		ctxID, ok := msg.Metadata["context_id"]
		if !ok || ctxID == "" {
			continue
		}

		if strings.HasPrefix(contextID, ctxID) {
			result = append(result, *msg)
		}
	}

	return result
}

func (s *Session) AppendMessage(ctx context.Context, ctxID string, message *types.Message) {
	nowAt := time.Now().Format(time.RFC3339)
	if message.Time == "" {
		message.Time = nowAt
	}

	if message.Metadata == nil {
		message.Metadata = make(map[string]string)
	}
	message.Metadata["context_id"] = ctxID
	err := s.store.AppendMessages(ctx, s.session.ID, message)
	if err != nil {
		s.logger.Warnw("failed to save message to store", zap.Error(err))
	}
}

func (s *Session) UpdateSummary(ctx context.Context, purpose, abstract string) error {
	s.session.Purpose = purpose
	s.session.Summary = abstract
	return s.store.UpdateSession(ctx, s.session)
}

func (s *Session) Notebook() Notebook {
	return s.notebook
}

func (s *Session) Tools() []*tools.Tool {
	return NotebookReadTools(s.notebook)
}

func (s *Session) Close() error {
	return s.RunHooks(context.Background(), types.SessionHookBeforeClosed, &types.SessionPayload{})
}
