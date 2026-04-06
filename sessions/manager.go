package sessions

import (
	"os"
	"strings"

	"github.com/basenana/friday/core/providers"
	coresession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/utils/logger"
)

// Manager handles session operations with current session tracking
type Manager struct {
	store       Store
	currentFile string
	tty         string
	llm         providers.Client
}

// NewManager creates a new session manager
func NewManager(store Store, currentFile string, tty string) *Manager {
	return &Manager{
		store:       store,
		currentFile: currentFile,
		tty:         tty,
	}
}

// GetStore returns the underlying store
func (m *Manager) GetStore() Store {
	return m.store
}

// SetLLM sets the LLM client for the manager
func (m *Manager) SetLLM(llm providers.Client) {
	m.llm = llm
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
		sess, err := m.store.Load(currentID, m.llm, opts...)
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
	alias := m.generateAlias()

	sess, err := m.store.Create(sessionID, m.llm, opts...)
	if err != nil {
		return nil, "", false, err
	}

	if alias != "" {
		if err = m.store.UpdateAlias(sessionID, alias); err != nil {
			return nil, "", false, err
		}
	}

	if err = m.SetCurrentID(sessionID); err != nil {
		return nil, "", false, err
	}

	return sess, sessionID, true, nil
}

// Alias returns the session alias based on current tty
func (m *Manager) Alias() string {
	if m.tty != "" {
		return "sess_" + m.tty
	}
	return ""
}

// generateAlias creates a session alias based on tty (internal use)
func (m *Manager) generateAlias() string {
	return m.Alias()
}

// GetOrCreateByID gets a session by ID, or creates it if it doesn't exist.
// Returns the session and whether it was newly created.
func (m *Manager) GetOrCreateByID(sessionID string, opts ...coresession.Option) (*coresession.Session, bool, error) {
	if err := m.store.EnsureDir(); err != nil {
		return nil, false, err
	}

	// Try to load existing session
	sess, err := m.store.Load(sessionID, m.llm, opts...)
	if err == nil {
		return sess, false, nil
	}

	// Create new session

	sess, err = m.store.Create(sessionID, m.llm, opts...)
	if err != nil {
		return nil, false, err
	}

	alias := m.generateAlias()
	if alias != "" {
		if err := m.store.UpdateAlias(sessionID, alias); err != nil {
			return nil, false, err
		}
	}

	if err := m.SetCurrentID(sessionID); err != nil {
		return nil, false, err
	}

	return sess, true, nil
}

// CreateIsolated creates a new session without setting it as current.
// Returns the session and session ID.
func (m *Manager) CreateIsolated(opts ...coresession.Option) (*coresession.Session, string, error) {
	if err := m.store.EnsureDir(); err != nil {
		return nil, "", err
	}

	sessionID := types.NewID()
	alias := m.generateAlias()

	sess, err := m.store.Create(sessionID, m.llm, opts...)
	if err != nil {
		return nil, "", err
	}

	if err := m.store.UpdateAlias(sessionID, alias); err != nil {
		return nil, "", err
	}

	return sess, sessionID, nil
}

// CreateTemporary creates a temporary session that won't persist messages.
// Returns the session and session ID.
func (m *Manager) CreateTemporary(opts ...coresession.Option) (*coresession.Session, string, error) {
	opts = append(opts, coresession.WithTemporary(true))
	sessionID := types.NewID()
	sess := coresession.New(sessionID, m.llm, opts...)
	return sess, sessionID, nil
}
