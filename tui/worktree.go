package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/basenana/friday/bus"
	codebasepkg "github.com/basenana/friday/coder/codebase"
	codercmds "github.com/basenana/friday/coder/commands"
	"github.com/basenana/friday/config"
	"github.com/basenana/friday/sessions"
	fridayworktree "github.com/basenana/friday/worktree"
)

type worktreeSwitchMsg struct {
	token       uint64
	path        string
	requirement string
	created     *fridayworktree.Created
	err         error
}

type worktreePreparedMsg struct {
	token   uint64
	runtime *preparedWorktreeRuntime
	err     error
}

type worktreeArchivePreparedMsg struct {
	token    uint64
	runtime  *preparedWorktreeRuntime
	removeID string
	err      error
}

type worktreeArchiveRemovedMsg struct {
	token      uint64
	worktreeID string
	err        error
}

func RunWorktree(sessMgr *sessions.Manager, cfg *config.Config, cwd string) error {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("get project directory: %w", err)
		}
	}
	service, err := fridayworktree.Open(context.Background(), cwd, cfg.WorktreePath(), cfg.Worktree.BranchPrefix, cfg.DataDirPath())
	if err != nil {
		return err
	}
	releaseOwner, err := codebasepkg.AcquireProjectLock(cfg.DataDirPath(), service.ProjectIdentity().ID, service.ProjectIdentity().Repository)
	if err != nil {
		return err
	}
	defer releaseOwner()
	current, err := service.Current(context.Background())
	if err != nil {
		return err
	}
	if err := service.Associate(current.Path, current.Branch, service.ProjectIdentity().ID, current.SessionID); err != nil {
		return err
	}
	supervisor, err := newWorktreeRuntimeSupervisor(service, sessMgr, cfg)
	if err != nil {
		return err
	}
	defer supervisor.Close()
	if err := supervisor.Restore(context.Background()); err != nil {
		return err
	}

	active, err := activateDefaultWorktree(context.Background(), supervisor, service)
	if err != nil {
		return err
	}
	activityFeed := supervisor.codebaseRuntime.ActivityFeed()
	defer activityFeed.Close()

	commands := codercmds.NewRegistry()
	codercmds.RegisterAll(commands)
	m := loadingModelAt(sessMgr, active.registry, commands, cfg, active.sessionID, active.workdir)
	m.runtime = sessMgr
	m.projectMgr = nil
	m.loopManager = active.loop
	m.worktreeMode = true
	m.worktreeService = service
	m.worktreeSupervisor = supervisor
	m.worktreeRuntime = active
	m.codebaseRuntime = supervisor.codebaseRuntime
	m.codebaseFeed = activityFeed
	m.codebaseActivities = map[string]codebasepkg.Activity{}
	m.refreshWorktreeTabs()
	m.resetWorktreeStatusFeeds()
	m.alternateScreen = useAlternateScreen(cfg.TUI.AlternateScreen)
	defer m.closeSession()
	final, runErr := tea.NewProgram(m).Run()
	if runErr != nil {
		return runErr
	}
	if result, ok := final.(*model); ok && result.fatalErr != nil {
		return result.fatalErr
	}
	return nil
}

func activateDefaultWorktree(ctx context.Context, supervisor *worktreeRuntimeSupervisor, service *fridayworktree.Service) (*worktreeRuntime, error) {
	target, err := defaultWorktreeTarget(ctx, service)
	if err != nil {
		return nil, err
	}
	return supervisor.Activate(ctx, target)
}

func defaultWorktreeTarget(ctx context.Context, service *fridayworktree.Service) (string, error) {
	items, err := service.List(ctx)
	if err != nil {
		return "", fmt.Errorf("list worktrees: %w", err)
	}
	for _, item := range items {
		if !item.Main || item.Stale {
			continue
		}
		if !item.Registered {
			if err := service.Associate(item.Path, item.Branch, service.ProjectIdentity().ID, item.SessionID); err != nil {
				return "", fmt.Errorf("register main worktree: %w", err)
			}
		}
		return item.ID, nil
	}
	return "", errors.New("main worktree is unavailable")
}

func samePath(a, b string) bool {
	aAbs, _ := filepath.Abs(a)
	bAbs, _ := filepath.Abs(b)
	aReal, aErr := filepath.EvalSymlinks(aAbs)
	bReal, bErr := filepath.EvalSymlinks(bAbs)
	if aErr == nil {
		aAbs = aReal
	}
	if bErr == nil {
		bAbs = bReal
	}
	return filepath.Clean(aAbs) == filepath.Clean(bAbs)
}

func (m *model) createWorktree(requirement string) tea.Cmd {
	m.worktreeChanging = true
	m.worktreeGeneration++
	token := m.worktreeGeneration
	supervisor := m.worktreeSupervisor
	width, height := m.width, m.height
	operation := func() tea.Msg {
		if supervisor == nil {
			return worktreePreparedMsg{token: token, err: fmt.Errorf("worktree runtime supervisor is unavailable")}
		}
		prepared, err := supervisor.prepareCreatedActivation(context.Background(), requirement, width, height)
		return worktreePreparedMsg{token: token, runtime: prepared, err: err}
	}
	return tea.Batch(operation, m.armSpinner())
}

func (m *model) selectWorktree(target string) tea.Cmd {
	if !m.canSwitchWorktree() {
		return nil
	}
	m.worktreeChanging = true
	m.worktreeGeneration++
	token := m.worktreeGeneration
	supervisor := m.worktreeSupervisor
	width, height := m.width, m.height
	operation := func() tea.Msg {
		if supervisor == nil {
			return worktreePreparedMsg{token: token, err: fmt.Errorf("worktree runtime supervisor is unavailable")}
		}
		prepared, err := supervisor.prepareActivation(context.Background(), target, width, height, "")
		return worktreePreparedMsg{token: token, runtime: prepared, err: err}
	}
	return tea.Batch(operation, m.armSpinner())
}

func (m *model) archiveCurrentWorktree() tea.Cmd {
	m.worktreeChanging = true
	m.worktreeGeneration++
	token := m.worktreeGeneration
	supervisor := m.worktreeSupervisor
	worktreeID := ""
	if m.worktreeRuntime != nil {
		worktreeID = m.worktreeRuntime.id
	}
	width, height := m.width, m.height
	operation := func() tea.Msg {
		if supervisor == nil || worktreeID == "" {
			return worktreeArchivePreparedMsg{token: token, err: errors.New("worktree runtime supervisor is unavailable")}
		}
		prepared, removeID, err := supervisor.prepareArchive(context.Background(), worktreeID, width, height)
		return worktreeArchivePreparedMsg{token: token, runtime: prepared, removeID: removeID, err: err}
	}
	return tea.Batch(operation, m.armSpinner())
}

func (m *model) changeMainWorktreeSession(targetSession, action string) tea.Cmd {
	m.worktreeChanging = true
	m.worktreeGeneration++
	token := m.worktreeGeneration
	supervisor := m.worktreeSupervisor
	worktreeID := ""
	if m.worktreeRuntime != nil {
		worktreeID = m.worktreeRuntime.id
	}
	width, height := m.width, m.height
	return func() tea.Msg {
		if supervisor == nil || worktreeID == "" {
			return worktreePreparedMsg{token: token, err: errors.New("main worktree runtime is unavailable")}
		}
		prepared, err := supervisor.prepareMainSession(context.Background(), worktreeID, targetSession, action, width, height)
		return worktreePreparedMsg{token: token, runtime: prepared, err: err}
	}
}

func (m *model) handleWorktreeArchivePrepared(msg worktreeArchivePreparedMsg) tea.Cmd {
	if msg.token != m.worktreeGeneration {
		if msg.runtime != nil && msg.runtime.feed != nil {
			msg.runtime.feed.Close()
		}
		return nil
	}
	if msg.err != nil {
		m.worktreeChanging = false
		m.appendBlock(chatBlock{kind: blockError, content: "archive worktree: " + msg.err.Error()})
		return nil
	}
	if msg.runtime == nil || msg.runtime.runtime == nil {
		m.worktreeChanging = false
		m.appendBlock(chatBlock{kind: blockError, content: "archive worktree: prepared runtime is unavailable"})
		return nil
	}
	switchCmd := m.commitPreparedWorktree(worktreePreparedMsg{token: msg.token, runtime: msg.runtime})
	if m.worktreeRuntime != msg.runtime.runtime {
		m.worktreeChanging = false
		return switchCmd
	}
	if msg.removeID == "" {
		m.worktreeChanging = false
		m.appendBlock(chatBlock{kind: blockDivider, content: "archived main worktree session"})
		return switchCmd
	}
	supervisor := m.worktreeSupervisor
	token, worktreeID := msg.token, msg.removeID
	removeCmd := func() tea.Msg {
		err := supervisor.removeArchived(context.Background(), worktreeID)
		return worktreeArchiveRemovedMsg{token: token, worktreeID: worktreeID, err: err}
	}
	return tea.Batch(switchCmd, removeCmd)
}

func (m *model) handleWorktreeArchiveRemoved(msg worktreeArchiveRemovedMsg) tea.Cmd {
	if msg.token != m.worktreeGeneration {
		return nil
	}
	m.worktreeChanging = false
	if msg.err != nil {
		m.appendBlock(chatBlock{kind: blockError, content: "archive worktree: " + msg.err.Error()})
	} else {
		m.appendBlock(chatBlock{kind: blockDivider, content: "archived worktree · " + shortID(msg.worktreeID)})
	}
	m.refreshWorktreeTabs()
	statusWaits := m.resetWorktreeStatusFeeds()
	m.layout()
	return tea.Batch(statusWaits...)
}

func (m *model) canSwitchWorktree() bool {
	return m.worktreeMode && !m.loading && m.fatalErr == nil && !m.planCompacting &&
		!m.manualCompacting && !m.reconciling && !m.worktreeChanging
}

func (m *model) openWorktreeSelector() {
	items, err := m.worktreeService.List(context.Background())
	if err != nil {
		m.appendBlock(chatBlock{kind: blockError, content: "list worktrees: " + err.Error()})
		return
	}
	selectorItems := make([]selectorItem, 0, len(items))
	for _, item := range items {
		if !item.Registered || item.Stale {
			continue
		}
		label := item.Branch
		if label == "" {
			label = "detached · " + shortID(item.HEAD)
		}
		marker := ""
		if samePath(item.Path, m.workdir) {
			marker = "current · "
		}
		selectorItems = append(selectorItems, selectorItem{value: item.Path, label: label, description: marker + item.Path})
	}
	m.selector = &selectorState{kind: selectorWorktree, title: "Select worktree", items: selectorItems}
}

// handleWorktreeSwitch remains as the path-resolution boundary used by older
// callers. Runtime construction is delegated to the retained supervisor.
func (m *model) handleWorktreeSwitch(msg worktreeSwitchMsg) tea.Cmd {
	if msg.token != m.worktreeGeneration {
		if msg.created != nil {
			_ = msg.created.Rollback(context.Background())
		}
		return nil
	}
	if msg.err != nil {
		m.worktreeChanging = false
		m.appendBlock(chatBlock{kind: blockError, content: "worktree: " + msg.err.Error()})
		return nil
	}
	if samePath(msg.path, m.workdir) {
		m.worktreeChanging = false
		m.worktreeRequirement = false
		return nil
	}
	token := msg.token
	supervisor := m.worktreeSupervisor
	width, height := m.width, m.height
	return func() tea.Msg {
		if supervisor == nil {
			return worktreePreparedMsg{token: token, err: fmt.Errorf("worktree runtime supervisor is unavailable")}
		}
		prepared, err := supervisor.prepareActivation(context.Background(), msg.path, width, height, msg.requirement)
		return worktreePreparedMsg{token: token, runtime: prepared, err: err}
	}
}

func (m *model) commitPreparedWorktree(msg worktreePreparedMsg) tea.Cmd {
	if msg.token != m.worktreeGeneration {
		if msg.runtime != nil && msg.runtime.feed != nil {
			msg.runtime.feed.Close()
		}
		return nil
	}
	if msg.err != nil {
		m.appendBlock(chatBlock{kind: blockError, content: "switch worktree: " + msg.err.Error()})
		return nil
	}
	prepared := msg.runtime
	if prepared == nil || prepared.runtime == nil {
		m.appendBlock(chatBlock{kind: blockError, content: "switch worktree: prepared runtime is unavailable"})
		return nil
	}
	if m.worktreeSupervisor == nil {
		if prepared.feed != nil {
			prepared.feed.Close()
		}
		m.appendBlock(chatBlock{kind: blockError, content: "switch worktree: worktree runtime supervisor is unavailable"})
		return nil
	}
	if err := m.worktreeSupervisor.commitActivation(prepared); err != nil {
		if prepared.feed != nil {
			prepared.feed.Close()
		}
		m.appendBlock(chatBlock{kind: blockError, content: "switch worktree: " + err.Error()})
		return nil
	}
	target := prepared.runtime
	newFeed := prepared.feed
	if newFeed == nil {
		newFeed = bus.SubscribeAgentFeed(target.bus, target.sessionID)
	}
	oldFeed := m.feed
	m.registry, m.runtime, m.projectMgr = target.registry, m.sessMgr, nil
	m.feed, m.sessionRelease, m.loopManager = newFeed, nil, target.loop
	m.worktreeRuntime = target
	m.sessionID, m.workdir = target.sessionID, target.workdir
	m.agentRegistry, m.skillRegistry = target.registry.AgentRegistry(), target.registry.SkillRegistry()
	m.mode, m.latestPlan, m.activeModel = prepared.mode, prepared.latestPlan, prepared.activeModel
	m.promptHistory, m.historyIndex = append([]string(nil), prepared.history...), -1
	m.textarea.Reset()
	m.menu = menuState{}
	m.worktreeRequirement = false
	m.loopActive = prepared.loopActive
	m.textarea.Placeholder = "Send a message…  / commands · Ctrl+P image · Ctrl+G editor"
	m.subscriptionToken++
	m.resetEventTracking()
	m.attachments, m.queued = nil, nil
	m.running, m.cancelling = false, false
	m.advanceDispatchForeground()
	m.resetStreaming()
	m.applyProjection(prepared.projection)
	m.running = prepared.running
	m.refreshWorktreeTabs()
	statusWaits := m.resetWorktreeStatusFeeds()
	m.restorePlanHandoff()
	if oldFeed != nil {
		oldFeed.Close()
	}
	m.appendBlock(chatBlock{kind: blockDivider, content: "worktree · " + filepath.Base(target.workdir)})
	m.layout()
	waitCmd := tea.Batch(append([]tea.Cmd{m.waitForActorEvent()}, statusWaits...)...)
	if strings.TrimSpace(prepared.requirement) != "" {
		m.recordPrompt(prepared.requirement)
		_, promptCmd := m.startUserTurn(prepared.requirement, nil)
		return tea.Batch(waitCmd, promptCmd)
	}
	return waitCmd
}
