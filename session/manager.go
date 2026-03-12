package session

import (
	"os"
	"strings"
	"time"

	coresession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/utils/logger"
)

// Manager handles session operations with current session tracking
type Manager struct {
	store       Store
	currentFile string
}

// NewManager creates a new session manager
func NewManager(store Store, currentFile string) *Manager {
	return &Manager{
		store:       store,
		currentFile: currentFile,
	}
}

// GetStore returns the underlying store
func (m *Manager) GetStore() Store {
	return m.store
}

// GetCurrentID returns the current session ID, or empty string if none
func (m *Manager) GetCurrentID() (string, error) {
	data, err := os.ReadFile(m.currentFile)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// SetCurrentID sets the current session ID
func (m *Manager) SetCurrentID(sessionID string) error {
	return os.WriteFile(m.currentFile, []byte(sessionID+"\n"), 0644)
}

// GetOrCreateCurrent gets the current session, or creates a new one if none exists.
// Returns the session, session ID, and whether it was newly created.
func (m *Manager) GetOrCreateCurrent(opts ...coresession.Option) (*coresession.Session, string, bool, error) {
	if err := m.store.EnsureDir(); err != nil {
		return nil, "", false, err
	}

	currentID, err := m.GetCurrentID()
	if err != nil {
		return nil, "", false, err
	}

	// Try to load existing session
	if currentID != "" {
		sess, err := m.store.Load(currentID, opts...)
		if err == nil {
			return sess, currentID, false, nil
		}
		// Session doesn't exist anymore, log warning and create new one
		logger.New("session").Warnw("current session load failed, creating new session",
			"session_id", currentID,
			"error", err,
		)
	}

	// Create new session
	sessionID := types.NewID()
	alias := time.Now().Format("2006-01-02")

	sess, err := m.store.Create(sessionID, opts...)
	if err != nil {
		return nil, "", false, err
	}

	if err := m.store.UpdateAlias(sessionID, alias); err != nil {
		return nil, "", false, err
	}

	if err := m.SetCurrentID(sessionID); err != nil {
		return nil, "", false, err
	}

	return sess, sessionID, true, nil
}

// GetOrCreateByID gets a session by ID, or creates it if it doesn't exist.
// Returns the session and whether it was newly created.
func (m *Manager) GetOrCreateByID(sessionID string, opts ...coresession.Option) (*coresession.Session, bool, error) {
	if err := m.store.EnsureDir(); err != nil {
		return nil, false, err
	}

	// Try to load existing session
	sess, err := m.store.Load(sessionID, opts...)
	if err == nil {
		return sess, false, nil
	}

	// Create new session
	alias := time.Now().Format("2006-01-02")

	sess, err = m.store.Create(sessionID, opts...)
	if err != nil {
		return nil, false, err
	}

	if err := m.store.UpdateAlias(sessionID, alias); err != nil {
		return nil, false, err
	}

	if err := m.SetCurrentID(sessionID); err != nil {
		return nil, false, err
	}

	return sess, true, nil
}
