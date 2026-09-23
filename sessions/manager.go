package sessions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/basenana/friday/core/collaboration"
	"github.com/basenana/friday/core/planning"
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

// CreateRoot creates a persisted top-level session using the explicitly
// supplied client and returns a lifecycle bound to it. Unlike legacy manager
// methods it never changes a global current pointer.
func (m *Manager) CreateRoot(_ context.Context, client providers.Client, opts ...coresession.Option) (SessionLifecycle, error) {
	if err := m.store.EnsureDir(); err != nil {
		return nil, err
	}
	id := types.NewID()
	sess, err := m.store.Create(id, client, opts...)
	if err != nil {
		return nil, err
	}
	if alias := m.generateAlias(); alias != "" {
		if err := m.store.UpdateAlias(id, alias); err != nil {
			cleanupErr := m.store.Delete(id)
			if cleanupErr != nil {
				return nil, fmt.Errorf("initialize root session: %w; cleanup: %v", err, cleanupErr)
			}
			return nil, err
		}
	}
	return newLifecycle(sess, client, m.store), nil
}

// OpenRoot opens an existing persisted top-level session. Missing IDs are
// returned as errors and are never created implicitly.
func (m *Manager) OpenRoot(_ context.Context, id string, client providers.Client, opts ...coresession.Option) (SessionLifecycle, error) {
	if err := m.store.EnsureDir(); err != nil {
		return nil, err
	}
	sess, err := m.store.Load(id, client, opts...)
	if err != nil {
		return nil, err
	}
	return newLifecycle(sess, client, m.store), nil
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
	sess, created, err := m.GetOrCreateDetachedByID(sessionID, opts...)
	if err != nil {
		return nil, false, err
	}
	if !created {
		return sess, false, nil
	}
	if err := m.SetCurrentID(sessionID); err != nil {
		return nil, false, err
	}
	return sess, true, nil
}

// GetOrCreateDetachedByID gets a session by ID, or creates it if it doesn't
// exist, without changing the manager's current session pointer.
func (m *Manager) GetOrCreateDetachedByID(sessionID string, opts ...coresession.Option) (*coresession.Session, bool, error) {
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
		if deleteErr := m.store.Delete(sessionID); deleteErr != nil {
			return nil, "", fmt.Errorf("initialize isolated session: %w; cleanup: %v", err, deleteErr)
		}
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

// Exists reports whether a persisted session with sessionID exists.
func (m *Manager) Exists(sessionID string) (bool, error) {
	if err := m.store.EnsureDir(); err != nil {
		return false, err
	}
	if _, err := m.store.GetMeta(sessionID); err != nil {
		if errors.Is(err, os.ErrNotExist) || os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// IsActive reports whether sessionID refers to a persisted, non-archived
// session. Missing sessions are a normal false result so scoped catalogs can
// repair their soft references without surfacing an error.
func (m *Manager) IsActive(sessionID string) (bool, error) {
	if err := m.store.EnsureDir(); err != nil {
		return false, err
	}
	meta, err := m.store.GetMeta(sessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return !meta.Archived, nil
}

// CollaborationMode implements collaboration.ModeProvider.
func (m *Manager) CollaborationMode(sessionID string) collaboration.Mode {
	meta, err := m.store.GetMeta(sessionID)
	if err != nil || meta.Runtime.Mode == "" {
		return collaboration.ModeDefault
	}
	mode, err := collaboration.ParseMode(string(meta.Runtime.Mode))
	if err != nil {
		return collaboration.ModeDefault
	}
	return mode
}

func (m *Manager) Runtime(sessionID string) (SessionRuntime, error) {
	meta, err := m.store.GetMeta(sessionID)
	if err != nil {
		return SessionRuntime{}, err
	}
	if meta.Runtime.Mode == "" {
		meta.Runtime.Mode = collaboration.ModeDefault
	}
	return meta.Runtime, nil
}

func (m *Manager) UpdateMeta(sessionID string, patch SessionMetaPatch) error {
	store, ok := m.store.(MetadataStore)
	if !ok {
		return errors.New("session store does not support mutable metadata")
	}
	return store.UpdateMeta(sessionID, patch)
}

func (m *Manager) SetMode(sessionID string, mode collaboration.Mode) error {
	parsed, err := collaboration.ParseMode(string(mode))
	if err != nil {
		return err
	}
	return m.UpdateMeta(sessionID, SessionMetaPatch{Mode: &parsed})
}

func (m *Manager) SetModel(sessionID string, model ModelSelection) error {
	model.Model = strings.TrimSpace(model.Model)
	if model.Model == "" {
		return errors.New("model is required")
	}
	return m.UpdateMeta(sessionID, SessionMetaPatch{Model: &model})
}

func (m *Manager) SetEffort(sessionID, effort string) error {
	effort = strings.ToLower(strings.TrimSpace(effort))
	if !providers.IsValidReasoningEffort(effort) {
		return fmt.Errorf("invalid reasoning effort %q: must be one of default, none, low, medium, high, xhigh, max", effort)
	}
	return m.UpdateMeta(sessionID, SessionMetaPatch{Effort: &effort})
}

// ClearModel removes the session override so future actors use the configured
// primary model. This is also used when a persisted selection no longer exists
// in the current configuration.
func (m *Manager) ClearModel(sessionID string) error {
	model := ModelSelection{}
	return m.UpdateMeta(sessionID, SessionMetaPatch{Model: &model})
}

func (m *Manager) ResolveActiveSession(target string) (*SessionMeta, error) {
	metas, err := m.store.ListActive()
	if err != nil {
		return nil, err
	}
	target = strings.TrimSpace(target)
	var matches []SessionMeta
	for _, meta := range metas {
		if meta.ID == target || meta.Name == target {
			copy := meta
			return &copy, nil
		}
	}
	for _, meta := range metas {
		if strings.HasPrefix(meta.ID, target) {
			matches = append(matches, meta)
		}
	}
	if len(matches) == 1 {
		return &matches[0], nil
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("ambiguous session prefix %q", target)
	}
	return nil, fmt.Errorf("session not found: %s", target)
}

func normalizeSessionName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, name)
	name = strings.TrimSpace(strings.Join(strings.Fields(name), " "))
	runes := []rune(name)
	if len(runes) > 200 {
		name = string(runes[:200])
	}
	return name
}

func (m *Manager) Rename(sessionID, requested string) (string, error) {
	base := normalizeSessionName(requested)
	if base == "" {
		return "", errors.New("session name is empty")
	}
	metas, err := m.store.List()
	if err != nil {
		return "", err
	}
	used := make(map[string]bool, len(metas))
	for _, meta := range metas {
		if meta.ID != sessionID && meta.Name != "" {
			used[meta.Name] = true
		}
	}
	name := base
	for n := 2; used[name]; n++ {
		name = fmt.Sprintf("%s (%d)", base, n)
	}
	return name, m.UpdateMeta(sessionID, SessionMetaPatch{Name: &name})
}

func (m *Manager) Archive(sessionID string) error {
	archived := true
	return m.UpdateMeta(sessionID, SessionMetaPatch{Archived: &archived})
}

// ArchiveIfExists archives sessionID when it still names a persisted session.
// Missing soft references are expected for worktree cleanup and are ignored.
func (m *Manager) ArchiveIfExists(sessionID string) (bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false, nil
	}
	exists, err := m.Exists(sessionID)
	if err != nil || !exists {
		return false, err
	}
	if err := m.Archive(sessionID); err != nil {
		return false, err
	}
	return true, nil
}

func (m *Manager) DeleteRoot(sessionID string) error {
	if relations, ok := m.store.(RelationStore); ok {
		items, err := relations.ListRelations(sessionID)
		if err != nil && !errors.Is(err, os.ErrNotExist) && !os.IsNotExist(err) {
			return err
		}
		for _, relation := range items {
			if err := m.store.Delete(relation.SessionID); err != nil && !errors.Is(err, os.ErrNotExist) && !os.IsNotExist(err) {
				return fmt.Errorf("delete associated session %s: %w", relation.SessionID, err)
			}
		}
	}
	return m.store.Delete(sessionID)
}

func (m *Manager) Delete(sessionID string) error { return m.DeleteRoot(sessionID) }

func (m *Manager) SavePlan(sessionID string, plan planning.Artifact) error {
	store, ok := m.store.(PlanningStore)
	if !ok {
		return errors.New("session store does not support plan artifacts")
	}
	return store.SavePlan(sessionID, plan)
}

func (m *Manager) ProposePlan(sessionID string, plan planning.Artifact) (*planning.Artifact, error) {
	store, ok := m.store.(PlanningStore)
	if !ok {
		return nil, errors.New("session store does not support plan artifacts")
	}
	return store.ProposePlan(sessionID, plan)
}

func (m *Manager) LoadPlan(sessionID, planID string) (*planning.Artifact, error) {
	store, ok := m.store.(PlanningStore)
	if !ok {
		return nil, errors.New("session store does not support plan artifacts")
	}
	return store.LoadPlan(sessionID, planID)
}

func (m *Manager) LoadLatestPlan(sessionID string) (*planning.Artifact, error) {
	store, ok := m.store.(PlanningStore)
	if !ok {
		return nil, nil
	}
	return store.LoadLatestPlan(sessionID)
}
