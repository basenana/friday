package session

import (
	"context"

	"github.com/basenana/friday/storehouse"
	"github.com/basenana/friday/types"
	"github.com/basenana/friday/utils/logger"
	"go.uber.org/zap"
)

type Manager struct {
	store  storehouse.Storehouse
	logger *zap.SugaredLogger
}

func (m *Manager) ListSessions(ctx context.Context, filter map[string]string) ([]*types.Session, error) {
	return nil, nil
}

func (m *Manager) OpenSession(ctx context.Context, sessionID string) (*types.Session, []*types.Message, error) {
	return nil, nil, nil
}

func (m *Manager) CloseSession(ctx context.Context, sessionID string) error {
	return nil
}

func (m *Manager) NewChatSession(ctx context.Context) (*types.Session, error) {
	return nil, nil
}

func (m *Manager) NewAgenticSession(ctx context.Context) (*types.Session, error) {
	return nil, nil
}

func (m *Manager) OpenSessionRecorder(ctx context.Context, session *types.Session) *Recorder {
	return &Recorder{
		session: session,
		store:   m.store,
		logger:  m.logger.With(zap.String("session", session.ID)),
	}
}

func New(store storehouse.Storehouse) *Manager {
	return &Manager{
		store:  store,
		logger: logger.New("session.manager"),
	}
}
