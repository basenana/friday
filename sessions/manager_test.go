package sessions

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/basenana/friday/core/planning"
	"github.com/basenana/friday/core/providers"
	coresession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
)

type baseOnlyStore struct{ Store }

var _ Store = (*baseOnlyStore)(nil)

func TestManagerOptionalStoreCapabilitiesRemainOptional(t *testing.T) {
	store := &baseOnlyStore{Store: newMockStore()}
	mgr := NewManager(store, filepath.Join(t.TempDir(), "current"), "")
	if err := mgr.SetMode("session", "plan"); err == nil || !strings.Contains(err.Error(), "mutable metadata") {
		t.Fatalf("SetMode error = %v", err)
	}
	if err := mgr.SavePlan("session", planning.Artifact{}); err == nil || !strings.Contains(err.Error(), "plan artifacts") {
		t.Fatalf("SavePlan error = %v", err)
	}
}

type aliasFailStore struct{ *mockStore }

func (s *aliasFailStore) UpdateAlias(string, string) error { return errors.New("alias failed") }

func TestManagerCreateIsolatedCleansUpInitializationFailure(t *testing.T) {
	store := &aliasFailStore{mockStore: newMockStore()}
	mgr := NewManager(store, filepath.Join(t.TempDir(), "current"), "test")
	if _, id, err := mgr.CreateIsolated(); err == nil || id != "" {
		t.Fatalf("CreateIsolated = id %q, err %v", id, err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.sessions) != 0 || len(store.metas) != 0 {
		t.Fatalf("failed isolated session leaked: sessions=%d metas=%d", len(store.sessions), len(store.metas))
	}
}

type mockStore struct {
	mu       sync.Mutex
	sessions map[string]*coresession.Session
	metas    map[string]*SessionMeta
	msgs     map[string][]types.Message
	plans    map[string]map[string]planning.Artifact
}

func newMockStore() *mockStore {
	return &mockStore{
		sessions: make(map[string]*coresession.Session),
		metas:    make(map[string]*SessionMeta),
		msgs:     make(map[string][]types.Message),
		plans:    make(map[string]map[string]planning.Artifact),
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

func (m *mockStore) ListActive() ([]SessionMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []SessionMeta
	for _, meta := range m.metas {
		if !meta.Archived {
			result = append(result, *meta)
		}
	}
	return result, nil
}

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

func (m *mockStore) UpdateMeta(sessionID string, patch SessionMetaPatch) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	meta := m.metas[sessionID]
	if meta == nil {
		return os.ErrNotExist
	}
	if patch.Name != nil {
		meta.Name = *patch.Name
	}
	if patch.Archived != nil {
		meta.Archived = *patch.Archived
	}
	if patch.Runtime != nil {
		meta.Runtime = *patch.Runtime
	}
	if patch.Mode != nil {
		meta.Runtime.Mode = *patch.Mode
	}
	if patch.Model != nil {
		meta.Runtime.Model = *patch.Model
	}
	if patch.LatestPlanID != nil {
		meta.LatestPlanID = *patch.LatestPlanID
	}
	return nil
}

func (m *mockStore) SavePlan(sessionID string, plan planning.Artifact) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.plans[sessionID] == nil {
		m.plans[sessionID] = map[string]planning.Artifact{}
	}
	m.plans[sessionID][plan.ID] = plan
	if meta := m.metas[sessionID]; meta != nil {
		meta.LatestPlanID = plan.ID
	}
	return nil
}

func (m *mockStore) ProposePlan(sessionID string, plan planning.Artifact) (*planning.Artifact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	plan.Version = 1
	if meta := m.metas[sessionID]; meta != nil && meta.LatestPlanID != "" {
		plan.Version = m.plans[sessionID][meta.LatestPlanID].Version + 1
	}
	if m.plans[sessionID] == nil {
		m.plans[sessionID] = map[string]planning.Artifact{}
	}
	m.plans[sessionID][plan.ID] = plan
	if meta := m.metas[sessionID]; meta != nil {
		meta.LatestPlanID = plan.ID
	}
	return &plan, nil
}

func (m *mockStore) LoadPlan(sessionID, planID string) (*planning.Artifact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	plan, ok := m.plans[sessionID][planID]
	if !ok {
		return nil, os.ErrNotExist
	}
	return &plan, nil
}

func (m *mockStore) LoadLatestPlan(sessionID string) (*planning.Artifact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	meta := m.metas[sessionID]
	if meta == nil || meta.LatestPlanID == "" {
		return nil, nil
	}
	plan, ok := m.plans[sessionID][meta.LatestPlanID]
	if !ok {
		return nil, os.ErrNotExist
	}
	return &plan, nil
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

func TestManager_GetOrCreateDetachedByID_DoesNotAffectCurrent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "manager_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	store := newMockStore()
	currentFile := filepath.Join(tmpDir, "current")
	mgr := NewManager(store, currentFile, "")

	_, currentID, _, err := mgr.GetOrCreateCurrent()
	if err != nil {
		t.Fatalf("GetOrCreateCurrent failed: %v", err)
	}

	detached, created, err := mgr.GetOrCreateDetachedByID("proposal-1")
	if err != nil {
		t.Fatalf("GetOrCreateDetachedByID create failed: %v", err)
	}
	if !created {
		t.Fatal("expected detached session to be newly created")
	}
	if detached.ID != "proposal-1" {
		t.Fatalf("detached session ID = %q, want proposal-1", detached.ID)
	}

	storedCurrentID, err := mgr.GetCurrentID()
	if err != nil {
		t.Fatalf("GetCurrentID failed: %v", err)
	}
	if storedCurrentID != currentID {
		t.Fatalf("current session should remain %s, got %s", currentID, storedCurrentID)
	}

	detached, created, err = mgr.GetOrCreateDetachedByID("proposal-1")
	if err != nil {
		t.Fatalf("GetOrCreateDetachedByID load failed: %v", err)
	}
	if created {
		t.Fatal("expected second detached lookup to load existing session")
	}
	if detached.ID != "proposal-1" {
		t.Fatalf("detached session ID = %q, want proposal-1", detached.ID)
	}
}

func TestManager_Exists(t *testing.T) {
	store := newMockStore()
	mgr := NewManager(store, filepath.Join(t.TempDir(), "current"), "")

	_, id, _, err := mgr.GetOrCreateCurrent()
	if err != nil {
		t.Fatalf("GetOrCreateCurrent failed: %v", err)
	}

	ok, err := mgr.Exists(id)
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if !ok {
		t.Fatalf("Exists(%q) = false, want true", id)
	}
}

func TestManager_ExistsMissing(t *testing.T) {
	store := newMockStore()
	mgr := NewManager(store, filepath.Join(t.TempDir(), "current"), "")

	ok, err := mgr.Exists("missing")
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if ok {
		t.Fatal("Exists(missing) = true, want false")
	}
}

func TestManagerRuntimeRenameAndLifecycle(t *testing.T) {
	store := newMockStore()
	mgr := NewManager(store, filepath.Join(t.TempDir(), "current"), "")
	for _, id := range []string{"one-abcdef", "two-abcdef"} {
		if _, _, err := mgr.GetOrCreateByID(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := mgr.SetMode("one-abcdef", "plan"); err != nil {
		t.Fatal(err)
	}
	selection := ModelSelection{Provider: "openai", Model: "gpt-test"}
	if err := mgr.SetModel("one-abcdef", selection); err != nil {
		t.Fatal(err)
	}
	runtimeState, err := mgr.Runtime("one-abcdef")
	if err != nil || runtimeState.Mode != "plan" || runtimeState.Model != selection {
		t.Fatalf("runtime = %+v, err=%v", runtimeState, err)
	}
	if err := mgr.ClearModel("one-abcdef"); err != nil {
		t.Fatal(err)
	}
	runtimeState, _ = mgr.Runtime("one-abcdef")
	if runtimeState.Model.Model != "" {
		t.Fatalf("model override not cleared: %+v", runtimeState)
	}

	name1, err := mgr.Rename("one-abcdef", "  Shared\n Name  ")
	if err != nil || name1 != "Shared Name" {
		t.Fatalf("first rename = %q, err=%v", name1, err)
	}
	name2, err := mgr.Rename("two-abcdef", "Shared Name")
	if err != nil || name2 != "Shared Name (2)" {
		t.Fatalf("deduplicated rename = %q, err=%v", name2, err)
	}
	resolved, err := mgr.ResolveActiveSession("one-")
	if err != nil || resolved.ID != "one-abcdef" {
		t.Fatalf("resolved = %+v, err=%v", resolved, err)
	}
	if err := mgr.Archive("one-abcdef"); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.ResolveActiveSession("one-abcdef"); err == nil {
		t.Fatal("archived session remained resumable")
	}
	if err := mgr.Delete("two-abcdef"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := mgr.Exists("two-abcdef"); ok {
		t.Fatal("deleted session still exists")
	}
}
