package sessions

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/basenana/friday/core/providers"
	coresession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
)

type mockStore struct {
	mu       sync.Mutex
	sessions map[string]*coresession.Session
	metas    map[string]*SessionMeta
	msgs     map[string][]types.Message
}

func newMockStore() *mockStore {
	return &mockStore{
		sessions: make(map[string]*coresession.Session),
		metas:    make(map[string]*SessionMeta),
		msgs:     make(map[string][]types.Message),
	}
}

func (m *mockStore) EnsureDir() error { return nil }

func (m *mockStore) AppendMessages(sessionID string, msgs ...types.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.msgs[sessionID] = append(m.msgs[sessionID], msgs...)
	return nil
}

func (m *mockStore) ReplaceMessages(sessionID string, msgs ...types.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.msgs[sessionID] = msgs
	return nil
}

func (m *mockStore) UpdateMessageTokens(sessionID string, updates map[int]int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	msgs := m.msgs[sessionID]
	for idx, tokens := range updates {
		if idx < 0 || idx >= len(msgs) {
			continue
		}
		msgs[idx].Tokens = tokens
	}
	m.msgs[sessionID] = msgs
	return nil
}

func (m *mockStore) Create(sessionID string, llm providers.Client, opts ...coresession.Option) (*coresession.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess := coresession.New(sessionID, llm, append(opts, coresession.WithMessageWriter(m))...)
	m.sessions[sessionID] = sess
	m.metas[sessionID] = &SessionMeta{ID: sessionID}
	return sess, nil
}

func (m *mockStore) Load(sessionID string, llm providers.Client, opts ...coresession.Option) (*coresession.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sess, ok := m.sessions[sessionID]; ok {
		return sess, nil
	}
	return nil, os.ErrNotExist
}

func (m *mockStore) Delete(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, sessionID)
	delete(m.metas, sessionID)
	delete(m.msgs, sessionID)
	return nil
}

func (m *mockStore) List() ([]SessionMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []SessionMeta
	for _, meta := range m.metas {
		result = append(result, *meta)
	}
	return result, nil
}

func (m *mockStore) ListActive() ([]SessionMeta, error) { return m.List() }

func (m *mockStore) GetMeta(sessionID string) (*SessionMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if meta, ok := m.metas[sessionID]; ok {
		return meta, nil
	}
	return nil, os.ErrNotExist
}

func (m *mockStore) UpdateAlias(sessionID, alias string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if meta, ok := m.metas[sessionID]; ok {
		meta.Alias = alias
	}
	return nil
}

func (m *mockStore) Archive(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if meta, ok := m.metas[sessionID]; ok {
		meta.Archived = true
	}
	return nil
}

func (m *mockStore) Unarchive(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if meta, ok := m.metas[sessionID]; ok {
		meta.Archived = false
	}
	return nil
}

func (m *mockStore) LoadMessages(sessionID string) ([]types.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.msgs[sessionID], nil
}

func TestManager_CreateIsolated(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "manager_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store := newMockStore()
	currentFile := filepath.Join(tmpDir, "current")
	mgr := NewManager(store, currentFile, "")

	sess, sessionID, err := mgr.CreateIsolated()
	if err != nil {
		t.Fatalf("CreateIsolated failed: %v", err)
	}

	if sess == nil {
		t.Fatal("expected session, got nil")
	}
	if sessionID == "" {
		t.Fatal("expected non-empty session ID")
	}

	currentID, err := mgr.GetCurrentID()
	if err != nil {
		t.Fatalf("GetCurrentID failed: %v", err)
	}
	if currentID != "" {
		t.Errorf("expected current session to be empty, got %s", currentID)
	}
}

func TestManager_CreateIsolated_DoesNotAffectCurrent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "manager_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store := newMockStore()
	currentFile := filepath.Join(tmpDir, "current")
	mgr := NewManager(store, currentFile, "")

	currentSess, currentID, _, err := mgr.GetOrCreateCurrent()
	if err != nil {
		t.Fatalf("GetOrCreateCurrent failed: %v", err)
	}

	isolatedSess, isolatedID, err := mgr.CreateIsolated()
	if err != nil {
		t.Fatalf("CreateIsolated failed: %v", err)
	}

	if isolatedID == currentID {
		t.Error("isolated session ID should differ from current session ID")
	}

	storedCurrentID, err := mgr.GetCurrentID()
	if err != nil {
		t.Fatalf("GetCurrentID failed: %v", err)
	}
	if storedCurrentID != currentID {
		t.Errorf("current session should remain %s, got %s", currentID, storedCurrentID)
	}

	if isolatedSess.ID == currentSess.ID {
		t.Error("isolated and current sessions should have different IDs")
	}
}
