package project

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/basenana/friday/core/collaboration"
	"github.com/basenana/friday/core/planning"
	"github.com/basenana/friday/core/providers"
	coresession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/sessions"
)

// Manager provides project-scoped root-session selection while delegating all
// real session persistence to the existing sessions.Manager.
type Manager struct {
	project  *Project
	sessions *sessions.Manager
}

func NewManager(project *Project, manager *sessions.Manager) *Manager {
	return &Manager{project: project, sessions: manager}
}

func (m *Manager) Project() *Project { return m.project }

func (m *Manager) CreateRoot(ctx context.Context, client providers.Client, opts ...coresession.Option) (sessions.SessionLifecycle, error) {
	lifecycle, err := m.sessions.CreateRoot(ctx, client, opts...)
	if err != nil {
		return nil, err
	}
	id := lifecycle.RootID()
	if err := m.project.AddSession(id); err != nil {
		_ = lifecycle.Close()
		cleanupErr := m.sessions.DeleteRoot(id)
		if cleanupErr != nil {
			return nil, fmt.Errorf("add project session: %w; cleanup: %v", err, cleanupErr)
		}
		return nil, err
	}
	return lifecycle, nil
}

func (m *Manager) OpenRoot(ctx context.Context, id string, client providers.Client, opts ...coresession.Option) (sessions.SessionLifecycle, error) {
	has, err := m.project.HasSession(id)
	if err != nil {
		return nil, err
	}
	if !has {
		return nil, fmt.Errorf("session is not referenced by project: %s", id)
	}
	return m.sessions.OpenRoot(ctx, id, client, opts...)
}

func (m *Manager) CurrentID() (string, error) {
	id, err := m.project.CurrentSessionID()
	if err != nil || id == "" {
		return "", err
	}
	has, err := m.project.HasSession(id)
	if err != nil || !has {
		return "", err
	}
	meta, err := m.sessions.GetStore().GetMeta(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || os.IsNotExist(err) {
			return "", nil
		}
		return "", nil // a soft reference must not make project startup fail
	}
	if meta.Archived {
		return "", nil
	}
	return id, nil
}

func (m *Manager) Activate(id string) error {
	has, err := m.project.HasSession(id)
	if err != nil {
		return err
	}
	if !has {
		return fmt.Errorf("session is not referenced by project: %s", id)
	}
	meta, err := m.sessions.GetStore().GetMeta(id)
	if err != nil {
		return err
	}
	if meta.Archived {
		return fmt.Errorf("cannot activate archived session: %s", id)
	}
	return m.project.SetCurrentSession(id)
}

func (m *Manager) Contains(id string) (bool, error) { return m.project.HasSession(id) }

func (m *Manager) List(activeOnly bool) ([]sessions.SessionMeta, error) {
	refs, err := m.project.ListSessionRefs()
	if err != nil {
		return nil, err
	}
	result := make([]sessions.SessionMeta, 0, len(refs))
	for _, ref := range refs {
		meta, err := m.sessions.GetStore().GetMeta(ref.SessionID)
		if err != nil {
			continue
		}
		if activeOnly && meta.Archived {
			continue
		}
		result = append(result, *meta)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].UpdatedAt.After(result[j].UpdatedAt) })
	return result, nil
}

func (m *Manager) Resolve(target string) (*sessions.SessionMeta, error) {
	items, err := m.List(true)
	if err != nil {
		return nil, err
	}
	target = strings.TrimSpace(target)
	for i := range items {
		if items[i].ID == target || items[i].Name == target {
			return &items[i], nil
		}
	}
	var matches []sessions.SessionMeta
	for _, item := range items {
		if strings.HasPrefix(item.ID, target) {
			matches = append(matches, item)
		}
	}
	if len(matches) == 1 {
		return &matches[0], nil
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("ambiguous session prefix %q", target)
	}
	return nil, fmt.Errorf("session not found in project: %s", target)
}

func normalizeName(name string) string {
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

func (m *Manager) Rename(id, requested string) (string, error) {
	if has, err := m.project.HasSession(id); err != nil || !has {
		if err != nil {
			return "", err
		}
		return "", fmt.Errorf("session is not referenced by project: %s", id)
	}
	base := normalizeName(requested)
	if base == "" {
		return "", fmt.Errorf("session name is empty")
	}
	items, err := m.List(false)
	if err != nil {
		return "", err
	}
	used := make(map[string]bool, len(items))
	for _, item := range items {
		if item.ID != id && item.Name != "" {
			used[item.Name] = true
		}
	}
	name := base
	for n := 2; used[name]; n++ {
		name = fmt.Sprintf("%s (%d)", base, n)
	}
	return name, m.sessions.UpdateMeta(id, sessions.SessionMetaPatch{Name: &name})
}

func (m *Manager) Archive(id string) error {
	if has, err := m.project.HasSession(id); err != nil || !has {
		if err != nil {
			return err
		}
		return fmt.Errorf("session is not referenced by project: %s", id)
	}
	return m.sessions.Archive(id)
}

func (m *Manager) DeleteRoot(id string) error {
	if has, err := m.project.HasSession(id); err != nil || !has {
		if err != nil {
			return err
		}
		return fmt.Errorf("session is not referenced by project: %s", id)
	}
	if err := m.sessions.DeleteRoot(id); err != nil {
		return err
	}
	if err := m.project.RemoveSession(id); err != nil {
		return err
	}
	current, err := m.project.CurrentSessionID()
	if err == nil && current == id {
		return m.project.SetCurrentSession("")
	}
	return err
}

func (m *Manager) GetMeta(id string) (*sessions.SessionMeta, error) {
	return m.sessions.GetStore().GetMeta(id)
}
func (m *Manager) UpdateMeta(id string, patch sessions.SessionMetaPatch) error {
	return m.sessions.UpdateMeta(id, patch)
}
func (m *Manager) Runtime(id string) (sessions.SessionRuntime, error) { return m.sessions.Runtime(id) }
func (m *Manager) CollaborationMode(id string) collaboration.Mode {
	return m.sessions.CollaborationMode(id)
}
func (m *Manager) SetMode(id string, mode collaboration.Mode) error {
	return m.sessions.SetMode(id, mode)
}
func (m *Manager) SetModel(id string, model sessions.ModelSelection) error {
	return m.sessions.SetModel(id, model)
}
func (m *Manager) ClearModel(id string) error { return m.sessions.ClearModel(id) }
func (m *Manager) SavePlan(id string, plan planning.Artifact) error {
	return m.sessions.SavePlan(id, plan)
}
func (m *Manager) ProposePlan(id string, plan planning.Artifact) (*planning.Artifact, error) {
	return m.sessions.ProposePlan(id, plan)
}
func (m *Manager) LoadPlan(id, planID string) (*planning.Artifact, error) {
	return m.sessions.LoadPlan(id, planID)
}
func (m *Manager) LoadLatestPlan(id string) (*planning.Artifact, error) {
	return m.sessions.LoadLatestPlan(id)
}
func (m *Manager) LoadUserHistory() ([]UserHistoryEntry, error) {
	return m.project.LoadUserHistory()
}
func (m *Manager) AppendUserHistory(text, sessionID string, createdAt time.Time) (bool, error) {
	return m.project.AppendUserHistory(text, sessionID, createdAt)
}
func (m *Manager) Base() *sessions.Manager  { return m.sessions }
func (m *Manager) GetStore() sessions.Store { return m.sessions.GetStore() }
