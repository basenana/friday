package sessions

import (
	"context"

	"github.com/basenana/friday/core/providers/openai"
	"github.com/basenana/friday/core/session"
	coretypes "github.com/basenana/friday/core/types"
	"github.com/basenana/friday/storehouse"
	"github.com/basenana/friday/types"
)

type Manager struct {
	store storehouse.Storehouse
}

func New(store storehouse.Storehouse) *Manager {
	return &Manager{store: store}
}

func (m *Manager) Create(ctx context.Context, sess *types.Session) (*types.Session, error) {
	return m.store.CreateSession(ctx, sess)
}

func (m *Manager) Get(ctx context.Context, sessionID string) (*types.Session, error) {
	return m.store.OpenSession(ctx, sessionID)
}

func (m *Manager) Update(ctx context.Context, sess *types.Session) error {
	return m.store.UpdateSession(ctx, sess)
}

func (m *Manager) Delete(ctx context.Context, sessionID string) error {
	return m.store.CloseSession(ctx, sessionID)
}

func (m *Manager) List(ctx context.Context, filter map[string]string, includesClosed bool) ([]*types.Session, error) {
	return m.store.ListSessions(ctx, filter, includesClosed)
}

func (m *Manager) Open(ctx context.Context, sessionID string, llm openai.Client, opts ...session.Option) (*session.Session, error) {
	_, err := m.store.OpenSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	messages, err := m.store.ListMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	history := make([]coretypes.Message, len(messages))
	for i, msg := range messages {
		history[i] = coretypes.Message(*msg)
	}

	options := append([]session.Option{session.WithHistory(history...)}, opts...)
	return session.New(sessionID, llm, options...), nil
}
