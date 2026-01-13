package sessions

import (
	"context"
	"sync"
	"time"

	tools2 "github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/utils/logger"
	"go.uber.org/zap"
)

type SessionStore interface {
	ListSessions(ctx context.Context, filter map[string]string, includesClosed bool) ([]*types.Session, error)
	CreateSession(ctx context.Context, session *types.Session) (*types.Session, error)
	UpdateSession(ctx context.Context, session *types.Session) error
	OpenSession(ctx context.Context, sessionID string) (*types.Session, error)
	AppendMessages(ctx context.Context, sessionID string, message ...*types.Message) error
	ListMessages(ctx context.Context, sessionID string) ([]*types.Message, error)
	CloseSession(ctx context.Context, sessionID string) error
}

type Manager struct {
	store  SessionStore
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

func (m *Manager) OpenSession(ctx context.Context, sessionID string) (*Descriptor, error) {
	s, err := m.store.OpenSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return newSessionDescriptor(s, m.store), nil
}

func (m *Manager) DeleteSession(ctx context.Context, sessionID string) error {
	return m.store.CloseSession(ctx, sessionID)
}

func NewManager(store SessionStore) *Manager {
	return &Manager{
		store:  store,
		logger: logger.New("session.manager"),
	}
}

type Descriptor struct {
	session    *types.Session
	store      SessionStore
	hooks      map[string][]HookHandler
	scratchpad tools2.Scratchpad
	mux        sync.Mutex
	logger     *zap.SugaredLogger
}

func newSessionDescriptor(s *types.Session, store SessionStore) *Descriptor {
	return &Descriptor{
		session:    s,
		store:      store,
		hooks:      make(map[string][]HookHandler),
		scratchpad: tools2.NewInMemoryScratchpad(),
		logger:     logger.New("session").With(zap.String("id", s.ID)),
	}
}

func (d *Descriptor) ID() string {
	return d.session.ID
}

func (d *Descriptor) History(ctx context.Context) []types.Message {
	allMessages, err := d.store.ListMessages(ctx, d.session.ID)
	if err != nil {
		d.logger.Errorw("failed to list session messages", zap.Error(err))
		return nil
	}

	var result []types.Message
	for _, msg := range allMessages {
		ctxID, ok := msg.Metadata["context_id"]
		if !ok || ctxID != d.session.ID { // ignore subagents message
			continue
		}

		result = append(result, *msg)
	}

	return result
}

func (d *Descriptor) AppendMessage(ctx context.Context, ctxID string, message *types.Message) {
	nowAt := time.Now().Format(time.RFC3339)
	if message.Time == "" {
		message.Time = nowAt
	}

	if message.Metadata == nil {
		message.Metadata = make(map[string]string)
	}
	message.Metadata["context_id"] = ctxID
	err := d.store.AppendMessages(ctx, d.session.ID, message)
	if err != nil {
		d.logger.Warnw("failed to save message to store", zap.Error(err))
	}
}

func (d *Descriptor) UpdateSummary(ctx context.Context, purpose, abstract string) error {
	d.session.Purpose = purpose
	d.session.Summary = abstract
	return d.store.UpdateSession(ctx, d.session)
}

func (d *Descriptor) Session() *types.Session {
	return d.session
}

func (d *Descriptor) Scratchpad() tools2.Scratchpad {
	return d.scratchpad
}

func (d *Descriptor) Tools() []*tools2.Tool {
	return tools2.ScratchpadReadTools(d.scratchpad)
}

func (d *Descriptor) Close() error {
	return d.RunHooks(context.Background(), types.SessionHookBeforeClosed, &types.SessionPayload{})
}
