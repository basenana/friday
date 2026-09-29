package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/actor"
	"github.com/basenana/friday/bus"
	codebasepkg "github.com/basenana/friday/coder/codebase"
	coderloop "github.com/basenana/friday/coder/loop"
	projectpkg "github.com/basenana/friday/coder/project"
	"github.com/basenana/friday/coder/worktreectx"
	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/collaboration"
	"github.com/basenana/friday/core/planning"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/sessions"
	"github.com/basenana/friday/setup"
	fridayworktree "github.com/basenana/friday/worktree"
)

// worktreeRuntime is the retained execution environment for one worktree.
// Foreground UI state (notably its event feed and transcript) deliberately
// lives outside this object so changing worktrees cannot stop background work.
type worktreeRuntime struct {
	id               string
	main             bool
	workdir          string
	projectResources string
	// sessionID is the foreground session of this runtime. One runtime serves a
	// whole scope, so the model keeps it in sync when the scope switches
	// sessions inside this runtime.
	sessionID string
	// manager is the scope-bound project session manager. It is the registry
	// catalog of this runtime and the scope's session pool.
	manager   *projectpkg.Manager
	registry  *actor.Registry
	bus       *eventbus.Bus
	loop      *coderloop.Manager
	lifecycle sessions.SessionLifecycle
	release   func()
	// replaces remains live until this runtime is successfully published. A
	// prepared replacement must not interrupt in-flight work in the runtime it
	// supersedes.
	replaces *worktreeRuntime
}

// preparedWorktreeRuntime contains the UI projection prepared before a
// foreground swap. Runtime-owned resources remain in worktreeRuntime.
type preparedWorktreeRuntime struct {
	runtime     *worktreeRuntime
	feed        *bus.Feed
	revision    uint64
	requirement string
	projection  transcriptProjection
	history     []string
	latestPlan  *planning.Artifact
	mode        collaboration.Mode
	activeModel config.ModelConfig
	loopActive  bool
	running     bool
}

type worktreeRuntimeCandidate struct {
	runtime *worktreeRuntime
	built   bool
}

// foregroundLifecycle returns the lifecycle of the session this runtime
// currently serves. A runtime hosts every session of its scope, so the
// build-time lease is only the fallback when the foreground session was
// switched inside the same registry.
func (r *worktreeRuntime) foregroundLifecycle() (sessions.SessionLifecycle, error) {
	if r == nil {
		return nil, errors.New("worktree runtime is unavailable")
	}
	if r.registry != nil && strings.TrimSpace(r.sessionID) != "" {
		if lifecycle, ok := r.registry.Lifecycle(r.sessionID); ok && lifecycle.Current() != nil {
			return lifecycle, nil
		}
	}
	if r.lifecycle != nil && r.lifecycle.Current() != nil {
		return r.lifecycle, nil
	}
	return nil, errors.New("restored session is unavailable")
}

// worktreeRuntimeSupervisor owns every opened runtime for one logical Git
// project. Runtime construction and foreground selection are serialized so a
// failed target can never displace the current runtime.
type worktreeRuntimeSupervisor struct {
	transitionMu sync.Mutex
	mu           sync.Mutex

	service    *fridayworktree.Service
	sessions   *sessions.Manager
	config     *config.Config
	store      fridayworktree.Store
	runtimes   map[string]*worktreeRuntime
	retired    []*worktreeRuntime
	active     *worktreeRuntime
	revision   uint64
	closed     bool
	projectBus *eventbus.Bus

	codebaseRuntime        *codebasepkg.Runtime
	codebaseProjectManager *projectpkg.Manager

	attachRuntime func(context.Context, *worktreeRuntime) error
}

func newWorktreeRuntimeSupervisor(service *fridayworktree.Service, sessMgr *sessions.Manager, cfg *config.Config) (*worktreeRuntimeSupervisor, error) {
	if service == nil {
		return nil, errors.New("worktree service is required")
	}
	if sessMgr == nil {
		return nil, errors.New("session manager is required")
	}
	if cfg == nil {
		return nil, errors.New("config is required")
	}
	store, err := fridayworktree.NewStore(cfg.ProjectsPath(), service.ProjectIdentity().ID)
	if err != nil {
		return nil, err
	}
	supervisor := &worktreeRuntimeSupervisor{
		service: service, sessions: sessMgr, config: cfg, store: store,
		runtimes: make(map[string]*worktreeRuntime), projectBus: eventbus.NewBus(),
	}
	if client, clientErr := setup.CreateProviderClient(cfg); clientErr == nil {
		service.SetNameGenerator(func(ctx context.Context, requirement string) (string, error) {
			return client.CompletionNonStreaming(ctx, providers.NewRequest(
				"Generate a concise English Git branch name for the requirement. Output only lowercase kebab-case ASCII, without a prefix or explanation, and use at most 20 characters.",
				types.Message{Role: types.RoleUser, Content: requirement},
			))
		})
	}
	supervisor.attachRuntime = supervisor.defaultAttachRuntime
	return supervisor, nil
}

// Restore discovers registered, non-stale worktrees and retains one runtime
// for each. Legacy worktree session references are adopted into the project
// session pool first. Recoverable loops resume through the same Attach path
// used by an explicit activation; worktrees without loop state remain idle.
func (s *worktreeRuntimeSupervisor) Restore(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.transitionMu.Lock()
	defer s.transitionMu.Unlock()
	if s.isClosed() {
		return errors.New("worktree runtime supervisor is closed")
	}
	if err := s.adoptLegacySessions(ctx); err != nil {
		return err
	}
	worktrees, err := s.service.List(ctx)
	if err != nil {
		return fmt.Errorf("list worktrees for restore: %w", err)
	}
	metadata, err := s.store.List()
	if err != nil {
		return fmt.Errorf("list worktree metadata for restore: %w", err)
	}

	candidates := make([]*worktreeRuntimeCandidate, 0, len(metadata))
	discard := func(cause error) error {
		for _, candidate := range candidates {
			cause = errors.Join(cause, s.discardCandidate(candidate))
		}
		return cause
	}
	for _, meta := range metadata {
		live := false
		for _, worktree := range worktrees {
			if !worktree.Stale && samePath(meta.Path, worktree.Path) {
				live = true
				break
			}
		}
		if !live {
			continue
		}
		candidate, err := s.resolveRuntime(ctx, meta.ID)
		if err != nil {
			return discard(fmt.Errorf("restore worktree %s: %w", meta.ID, err))
		}
		candidates = append(candidates, candidate)
		runtime := candidate.runtime
		lifecycle, err := runtime.foregroundLifecycle()
		if err != nil {
			return discard(fmt.Errorf("restore worktree %s: %w", meta.ID, err))
		}
		active, err := runtime.loop.IsActive(ctx, lifecycle.Current())
		if err != nil {
			return discard(fmt.Errorf("inspect worktree loop %s: %w", meta.ID, err))
		}
		if active {
			if err := s.attach(ctx, runtime); err != nil {
				return discard(fmt.Errorf("attach worktree loop %s: %w", meta.ID, err))
			}
		}
	}
	for _, candidate := range candidates {
		s.installCandidate(candidate)
	}
	return nil
}

// adoptLegacySessions imports older one-session-per-worktree references into
// the project session pool. It is idempotent, and after the first successful
// run every legacy reference is empty so the call becomes a no-op.
func (s *worktreeRuntimeSupervisor) adoptLegacySessions(ctx context.Context) error {
	project, err := projectpkg.OpenWithIdentity(s.service.ProjectCodeRoot(), s.service.ProjectIdentity(), projectpkg.NewFileStore(s.config.ProjectsPath()))
	if err != nil {
		return fmt.Errorf("open project for session adoption: %w", err)
	}
	if err := fridayworktree.AdoptSessions(ctx, s.store, s.service.ProjectCodeRoot(), project, s.sessions); err != nil {
		return fmt.Errorf("adopt legacy worktree sessions: %w", err)
	}
	return nil
}

// Activate opens or reuses one retained runtime and makes it the supervisor's
// foreground. No resource belonging to the previous runtime is torn down.
func (s *worktreeRuntimeSupervisor) Activate(ctx context.Context, worktreeID string) (*worktreeRuntime, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.transitionMu.Lock()
	defer s.transitionMu.Unlock()
	if s.isClosed() {
		return nil, errors.New("worktree runtime supervisor is closed")
	}
	candidate, err := s.resolveRuntime(ctx, worktreeID)
	if err != nil {
		return nil, err
	}
	if err := s.attach(ctx, candidate.runtime); err != nil {
		cleanupErr := s.discardCandidate(candidate)
		return nil, errors.Join(fmt.Errorf("attach worktree loop: %w", err), cleanupErr)
	}
	codebaseTransition, err := s.prepareCodebaseTransition(ctx, candidate.runtime)
	if err != nil {
		cleanupErr := s.discardCandidate(candidate)
		return nil, errors.Join(fmt.Errorf("prepare Codebase worktree transition: %w", err), cleanupErr)
	}
	s.installCandidate(candidate)
	s.publish(candidate.runtime)
	if codebaseTransition != nil {
		codebaseTransition.Commit()
	}
	s.closeCommittedReplacement(candidate.runtime)
	return candidate.runtime, nil
}

// Create creates a Git worktree and retains its runtime. Any failure before
// the runtime becomes owned rolls back the checkout, branch, metadata, and a
// newly allocated session.
func (s *worktreeRuntimeSupervisor) Create(ctx context.Context, requirement string) (*worktreeRuntime, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.transitionMu.Lock()
	defer s.transitionMu.Unlock()
	if s.isClosed() {
		return nil, errors.New("worktree runtime supervisor is closed")
	}
	created, err := s.service.Create(ctx, requirement)
	if err != nil {
		return nil, err
	}
	candidate, err := s.resolveRuntime(ctx, created.Worktree.Path)
	if err == nil {
		err = s.attach(ctx, candidate.runtime)
	}
	var codebaseTransition *codebasepkg.SessionTransition
	if err == nil {
		codebaseTransition, err = s.prepareCodebaseTransition(ctx, candidate.runtime)
	}
	if err == nil {
		s.installCandidate(candidate)
		s.publish(candidate.runtime)
		if codebaseTransition != nil {
			codebaseTransition.Commit()
		}
		s.closeCommittedReplacement(candidate.runtime)
		return candidate.runtime, nil
	}
	if candidate != nil {
		err = errors.Join(err, s.discardCandidate(candidate))
	}
	if rollbackErr := created.Rollback(context.Background()); rollbackErr != nil {
		err = errors.Join(err, fmt.Errorf("rollback worktree creation: %w", rollbackErr))
	}
	return nil, err
}

func (s *worktreeRuntimeSupervisor) resolveRuntime(ctx context.Context, worktreeID string) (*worktreeRuntimeCandidate, error) {
	meta, err := s.store.Get(worktreeID)
	if err != nil {
		return nil, err
	}
	if err := validateWorktreeRuntimePath(meta.Path); err != nil {
		return nil, err
	}
	s.mu.Lock()
	runtime := s.runtimes[meta.ID]
	s.mu.Unlock()
	reusable, err := s.runtimeReusable(ctx, meta, runtime)
	if err != nil {
		return nil, err
	}
	if reusable {
		if err := s.store.Touch(meta.ID); err != nil {
			return nil, fmt.Errorf("touch worktree metadata: %w", err)
		}
		return &worktreeRuntimeCandidate{runtime: runtime}, nil
	}

	runtime, err = s.buildRuntime(ctx, meta)
	if err != nil {
		return nil, err
	}
	candidate := &worktreeRuntimeCandidate{runtime: runtime, built: true}
	if err := s.store.Touch(meta.ID); err != nil {
		cleanupErr := s.discardCandidate(candidate)
		return nil, errors.Join(fmt.Errorf("touch worktree metadata: %w", err), cleanupErr)
	}
	return candidate, nil
}

// runtimeReusable reports whether a retained runtime still serves its scope.
// One runtime hosts every session of its scope, so a retained runtime stays
// valid until its foreground session becomes unusable.
func (s *worktreeRuntimeSupervisor) runtimeReusable(ctx context.Context, meta fridayworktree.Metadata, runtime *worktreeRuntime) (bool, error) {
	if runtime == nil || runtime.registry == nil || runtime.lifecycle == nil || runtime.loop == nil {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	sessionID := strings.TrimSpace(runtime.sessionID)
	if sessionID == "" {
		return false, nil
	}
	if _, err := runtime.foregroundLifecycle(); err != nil {
		return false, nil
	}
	active, err := s.sessions.IsActive(sessionID)
	if err != nil {
		return false, fmt.Errorf("validate worktree session: %w", err)
	}
	if !active {
		return false, nil
	}
	if _, release, err := runtime.registry.AcquireLifecycle(sessionID); err != nil {
		return false, nil
	} else {
		release()
	}
	return true, nil
}

func (s *worktreeRuntimeSupervisor) buildRuntime(ctx context.Context, meta fridayworktree.Metadata) (*worktreeRuntime, error) {
	project, err := projectpkg.OpenWithIdentity(meta.Path, s.service.ProjectIdentity(), projectpkg.NewFileStore(s.config.ProjectsPath()))
	if err != nil {
		return nil, fmt.Errorf("open worktree project: %w", err)
	}
	manager := projectpkg.NewScopedManager(project, s.sessions, fridayworktree.ScopeForWorktree(meta, s.service.ProjectCodeRoot()))
	sessionID, err := s.ensureScopeSession(ctx, manager)
	if err != nil {
		return nil, err
	}
	return s.buildRuntimeWithManager(ctx, meta, project, manager, sessionID)
}

// ensureScopeSession returns the current session of one scope and creates the
// first one when the scope has none yet. The scope pointer is the only durable
// state a runtime needs, so a soft or dangling pointer simply yields a fresh
// session.
func (s *worktreeRuntimeSupervisor) ensureScopeSession(ctx context.Context, manager *projectpkg.Manager) (string, error) {
	current, err := manager.CurrentID()
	if err != nil {
		return "", fmt.Errorf("resolve worktree scope session: %w", err)
	}
	if current != "" {
		return current, nil
	}
	lifecycle, err := manager.CreateRoot(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("create worktree scope session: %w", err)
	}
	id := lifecycle.RootID()
	if err := lifecycle.Close(); err != nil {
		return "", fmt.Errorf("close worktree scope session bootstrap: %w", err)
	}
	if err := manager.Activate(id); err != nil {
		return "", fmt.Errorf("activate worktree scope session: %w", err)
	}
	return id, nil
}

// scopeManagerFor returns the scope-bound session manager of one worktree. It
// lets removal archive a scope without a retained runtime.
func (s *worktreeRuntimeSupervisor) scopeManagerFor(meta fridayworktree.Metadata) (*projectpkg.Manager, error) {
	project, err := projectpkg.OpenWithIdentity(s.service.ProjectCodeRoot(), s.service.ProjectIdentity(), projectpkg.NewFileStore(s.config.ProjectsPath()))
	if err != nil {
		return nil, fmt.Errorf("open worktree project: %w", err)
	}
	return projectpkg.NewScopedManager(project, s.sessions, fridayworktree.ScopeForWorktree(meta, s.service.ProjectCodeRoot())), nil
}

func (s *worktreeRuntimeSupervisor) buildRuntimeWithManager(ctx context.Context, meta fridayworktree.Metadata, project *projectpkg.Project, manager *projectpkg.Manager, sessionID string) (*worktreeRuntime, error) {
	resources := filepath.Join(s.config.ProjectsPath(), project.ID())
	registryConfig := actor.DefaultRegistryConfig()
	registryConfig.AgentPlanEntry = true
	registryConfig.ConfigTools = true
	registryConfig.Catalog = manager
	registryConfig.Workdir = project.Root()
	registryConfig.ProjectResources = resources
	registryConfig.ProjectCodeRoot = s.service.ProjectCodeRoot()
	registryConfig.WorktreeContext = &worktreectx.Context{
		ProjectName: s.service.ProjectIdentity().Name, ProjectRoot: s.service.ProjectCodeRoot(),
		WorktreeName: meta.Name, Branch: meta.Branch, WorktreeRoot: project.Root(), SessionID: sessionID,
	}
	registryConfig.Bus = s.projectBus
	registry, err := actor.NewRegistry(s.sessions, s.config, registryConfig)
	if err != nil {
		return nil, err
	}
	keepRegistry := false
	defer func() {
		if !keepRegistry {
			registry.ShutdownAll()
		}
	}()
	lifecycle, release, err := registry.AcquireLifecycle(sessionID)
	if err != nil {
		return nil, err
	}
	loop := coderloop.NewManager(
		registry.Bus(),
		coderloop.WithInputDispatcher(registry.DispatchInput),
		coderloop.WithActorLease(func(sessionID string) (func(), error) {
			_, release, leaseErr := registry.AcquireLifecycle(sessionID)
			return release, leaseErr
		}),
	)
	builtRuntime := &worktreeRuntime{
		id: meta.ID, main: samePath(meta.Path, s.service.ProjectCodeRoot()), workdir: project.Root(), projectResources: resources,
		sessionID: sessionID, manager: manager, registry: registry,
		bus: registry.Bus(), loop: loop, lifecycle: lifecycle, release: release,
	}
	if err := s.attachCodebase(ctx, project, builtRuntime); err != nil {
		closeWorktreeRuntime(builtRuntime)
		keepRegistry = true
		return nil, err
	}
	keepRegistry = true
	return builtRuntime, nil
}

func (s *worktreeRuntimeSupervisor) attachCodebase(ctx context.Context, project *projectpkg.Project, runtime *worktreeRuntime) error {
	if s.codebaseRuntime == nil {
		manager := projectpkg.NewManager(project, s.sessions)
		codebaseRuntime, err := codebasepkg.New(codebasepkg.Options{
			DataDir: s.config.DataDirPath(), Project: project, ProjectManager: manager,
			ModelPool: runtime.registry.ModelPool(), Bus: s.projectBus, Sandbox: s.config.Sandbox,
		})
		if err != nil {
			return fmt.Errorf("construct project Codebase runtime: %w", err)
		}
		if err := codebaseRuntime.Attach(runtime.lifecycle); err != nil {
			_ = codebaseRuntime.Close()
			return fmt.Errorf("attach project Codebase hook: %w", err)
		}
		if err := codebaseRuntime.Start(ctx, runtime.sessionID); err != nil {
			codebaseRuntime.Detach(runtime.lifecycle)
			_ = codebaseRuntime.Close()
			return fmt.Errorf("start project Codebase runtime: %w", err)
		}
		s.codebaseRuntime = codebaseRuntime
		s.codebaseProjectManager = manager
		return nil
	}
	if err := s.codebaseRuntime.Attach(runtime.lifecycle); err != nil {
		return fmt.Errorf("attach project Codebase hook: %w", err)
	}
	return nil
}

func (s *worktreeRuntimeSupervisor) prepareCodebaseTransition(ctx context.Context, runtime *worktreeRuntime) (*codebasepkg.SessionTransition, error) {
	if s.codebaseRuntime == nil || runtime == nil {
		return nil, nil
	}
	return s.codebaseRuntime.PrepareSessionSwitch(ctx, runtime.sessionID)
}

// prepareActivation performs every operation that can fail before the model
// swaps foreground pointers. A newly built target is retained only after its
// complete preparation succeeds; the foreground is published separately.
func (s *worktreeRuntimeSupervisor) prepareActivation(ctx context.Context, worktreeID string, width, height int, requirement string) (*preparedWorktreeRuntime, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.transitionMu.Lock()
	defer s.transitionMu.Unlock()
	if s.isClosed() {
		return nil, errors.New("worktree runtime supervisor is closed")
	}
	revision := s.currentRevision()
	candidate, err := s.resolveRuntime(ctx, worktreeID)
	if err != nil {
		return nil, err
	}
	prepared, err := s.prepareRuntimeActivation(ctx, candidate.runtime, width, height, requirement)
	if err != nil {
		cleanupErr := s.discardCandidate(candidate)
		return nil, errors.Join(err, cleanupErr)
	}
	s.installCandidate(candidate)
	prepared.revision = revision
	return prepared, nil
}

func (s *worktreeRuntimeSupervisor) prepareCreatedActivation(ctx context.Context, requirement string, width, height int) (*preparedWorktreeRuntime, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.transitionMu.Lock()
	defer s.transitionMu.Unlock()
	if s.isClosed() {
		return nil, errors.New("worktree runtime supervisor is closed")
	}
	revision := s.currentRevision()
	created, err := s.service.Create(ctx, requirement)
	if err != nil {
		return nil, err
	}
	candidate, err := s.resolveRuntime(ctx, created.Worktree.Path)
	if err == nil {
		var prepared *preparedWorktreeRuntime
		prepared, err = s.prepareRuntimeActivation(ctx, candidate.runtime, width, height, requirement)
		if err == nil {
			s.installCandidate(candidate)
			prepared.revision = revision
			return prepared, nil
		}
	}
	if candidate != nil {
		err = errors.Join(err, s.discardCandidate(candidate))
	}
	if rollbackErr := created.Rollback(context.Background()); rollbackErr != nil {
		err = errors.Join(err, fmt.Errorf("rollback worktree creation: %w", rollbackErr))
	}
	return nil, err
}

func (s *worktreeRuntimeSupervisor) prepareRuntimeActivation(ctx context.Context, runtime *worktreeRuntime, width, height int, requirement string) (*preparedWorktreeRuntime, error) {
	lifecycle, err := runtime.foregroundLifecycle()
	if err != nil {
		return nil, err
	}
	feed := bus.SubscribeAgentFeed(runtime.bus, runtime.sessionID)
	keepFeed := false
	defer func() {
		if !keepFeed {
			feed.Close()
		}
	}()
	if err := normalizeSessionModel(s.sessions, s.config, runtime.sessionID); err != nil {
		return nil, fmt.Errorf("validate session model: %w", err)
	}
	running := runtime.registry.SessionRunning(runtime.sessionID)
	pendingForms := runtime.registry.SessionPendingForms(runtime.sessionID)
	projection, err := buildRuntimeTranscriptProjection(s.sessions, s.config, runtime.workdir, width, height, runtime.sessionID, pendingForms)
	if err != nil {
		return nil, fmt.Errorf("restore transcript: %w", err)
	}
	latestPlan, err := s.sessions.LoadLatestPlan(runtime.sessionID)
	if err != nil {
		return nil, fmt.Errorf("restore plan: %w", err)
	}
	loopActive, err := runtime.loop.IsActive(ctx, lifecycle.Current())
	if err != nil {
		return nil, fmt.Errorf("restore loop status: %w", err)
	}
	if err := s.attach(ctx, runtime); err != nil {
		return nil, fmt.Errorf("attach worktree loop: %w", err)
	}
	activeModel, _ := configuredSessionModel(s.sessions, s.config, runtime.sessionID)
	keepFeed = true
	return &preparedWorktreeRuntime{
		runtime: runtime, feed: feed, requirement: requirement, projection: projection,
		history: projection.promptHistory, latestPlan: latestPlan,
		mode: s.sessions.CollaborationMode(runtime.sessionID), activeModel: activeModel,
		loopActive: loopActive, running: running,
	}, nil
}

func (s *worktreeRuntimeSupervisor) commitActivation(prepared *preparedWorktreeRuntime) error {
	if prepared == nil || prepared.runtime == nil {
		return errors.New("prepared worktree runtime is unavailable")
	}
	s.transitionMu.Lock()
	defer s.transitionMu.Unlock()
	if s.isClosed() {
		return errors.New("worktree runtime supervisor is closed")
	}
	runtime := prepared.runtime
	s.mu.Lock()
	revision := s.revision
	retained := s.runtimes[runtime.id]
	s.mu.Unlock()
	if prepared.revision != revision || retained != runtime {
		return errors.New("prepared worktree activation is stale")
	}
	meta, err := s.store.Get(runtime.id)
	if err != nil {
		return fmt.Errorf("validate prepared worktree: %w", err)
	}
	reusable, err := s.runtimeReusable(context.Background(), meta, runtime)
	if err != nil {
		return err
	}
	if !reusable {
		return errors.New("prepared worktree runtime is unavailable")
	}
	codebaseTransition, err := s.prepareCodebaseTransition(context.Background(), runtime)
	if err != nil {
		return fmt.Errorf("prepare Codebase worktree transition: %w", err)
	}
	transitionCommitted := false
	defer func() {
		if codebaseTransition != nil && !transitionCommitted {
			codebaseTransition.Abort()
		}
	}()
	s.publish(runtime)
	if codebaseTransition != nil {
		codebaseTransition.Commit()
	}
	transitionCommitted = true
	s.closeCommittedReplacement(runtime)
	return nil
}

func (s *worktreeRuntimeSupervisor) defaultAttachRuntime(ctx context.Context, runtime *worktreeRuntime) error {
	lifecycle, err := runtime.foregroundLifecycle()
	if err != nil {
		return err
	}
	return runtime.loop.Attach(ctx, lifecycle.Current())
}

func (s *worktreeRuntimeSupervisor) attach(ctx context.Context, runtime *worktreeRuntime) error {
	if s.attachRuntime == nil {
		return s.defaultAttachRuntime(ctx, runtime)
	}
	return s.attachRuntime(ctx, runtime)
}

func (s *worktreeRuntimeSupervisor) installCandidate(candidate *worktreeRuntimeCandidate) {
	if candidate == nil || !candidate.built || candidate.runtime == nil {
		return
	}
	s.mu.Lock()
	if previous := s.runtimes[candidate.runtime.id]; previous != nil && previous != candidate.runtime {
		candidate.runtime.replaces = previous
		s.retired = append(s.retired, previous)
	}
	s.runtimes[candidate.runtime.id] = candidate.runtime
	s.mu.Unlock()
}

// closeCommittedReplacement reclaims a same-checkout runtime only after its
// replacement has passed every validation and become the published runtime.
// Until then the old registry, lifecycle lease, and loop remain usable in the
// background, preserving activation failure atomicity.
func (s *worktreeRuntimeSupervisor) closeCommittedReplacement(runtime *worktreeRuntime) {
	if runtime == nil {
		return
	}
	s.mu.Lock()
	obsolete := runtime.replaces
	runtime.replaces = nil
	if obsolete != nil {
		for i, retired := range s.retired {
			if retired == obsolete {
				s.retired = append(s.retired[:i], s.retired[i+1:]...)
				break
			}
		}
	}
	s.mu.Unlock()
	if obsolete != nil && s.codebaseRuntime != nil {
		s.codebaseRuntime.Detach(obsolete.lifecycle)
	}
	closeWorktreeRuntime(obsolete)
}

func (s *worktreeRuntimeSupervisor) discardCandidate(candidate *worktreeRuntimeCandidate) error {
	if candidate == nil || !candidate.built || candidate.runtime == nil {
		return nil
	}
	if s.codebaseRuntime != nil {
		s.codebaseRuntime.Detach(candidate.runtime.lifecycle)
	}
	closeWorktreeRuntime(candidate.runtime)
	return nil
}

func (s *worktreeRuntimeSupervisor) publish(runtime *worktreeRuntime) {
	s.service.SetCurrentCanonicalPath(runtime.workdir)
	s.mu.Lock()
	s.active = runtime
	s.revision++
	s.mu.Unlock()
}

func (s *worktreeRuntimeSupervisor) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *worktreeRuntimeSupervisor) currentRevision() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revision
}

func (s *worktreeRuntimeSupervisor) runtimeSnapshot() map[string]*worktreeRuntime {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]*worktreeRuntime, len(s.runtimes))
	for id, runtime := range s.runtimes {
		out[id] = runtime
	}
	return out
}

// prepareArchive prepares the foreground transition to the main checkout
// before one linked worktree is removed. Archiving the main checkout itself is
// an ordinary session archive, so it never reaches this path.
func (s *worktreeRuntimeSupervisor) prepareArchive(ctx context.Context, worktreeID string, width, height int) (*preparedWorktreeRuntime, string, error) {
	items, err := s.service.List(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("list worktrees for archive: %w", err)
	}
	mainID := ""
	targetMain := false
	for _, item := range items {
		if item.Main && item.Registered && !item.Stale {
			mainID = item.ID
		}
		if item.ID == worktreeID {
			targetMain = item.Main
		}
	}
	if mainID == "" {
		return nil, "", errors.New("main worktree is unavailable")
	}
	if targetMain {
		return nil, "", errors.New("the main checkout cannot be archived; archive its session instead")
	}
	prepared, err := s.prepareActivation(ctx, mainID, width, height, "")
	return prepared, worktreeID, err
}

func (s *worktreeRuntimeSupervisor) removeArchived(ctx context.Context, worktreeID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.transitionMu.Lock()
	defer s.transitionMu.Unlock()
	if s.isClosed() {
		return errors.New("worktree runtime supervisor is closed")
	}
	s.mu.Lock()
	runtime := s.runtimes[worktreeID]
	if runtime != nil && s.active == runtime {
		s.mu.Unlock()
		return errors.New("cannot remove the active worktree runtime")
	}
	delete(s.runtimes, worktreeID)
	s.revision++
	s.mu.Unlock()
	meta, err := s.store.Get(worktreeID)
	if err != nil {
		return fmt.Errorf("load worktree before removal: %w", err)
	}
	pool, err := s.scopeManagerFor(meta)
	if err != nil {
		return err
	}
	if runtime != nil {
		if s.codebaseRuntime != nil {
			s.codebaseRuntime.Detach(runtime.lifecycle)
		}
		closeWorktreeRuntime(runtime)
	}
	// Every session of the removed scope becomes unreachable, so removal
	// archives the whole scope instead of a single legacy reference.
	return s.service.Remove(ctx, worktreeID, fridayworktree.RemoveOptions{Sessions: pool})
}

// Close stops all loop managers before releasing lifecycle leases and shutting
// down actor registries. The ordering lets active loops persist suspension
// before their actors disappear.
func (s *worktreeRuntimeSupervisor) Close() (retErr error) {
	s.transitionMu.Lock()
	defer s.transitionMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.revision++
	seen := make(map[*worktreeRuntime]bool, len(s.runtimes)+len(s.retired))
	runtimes := make([]*worktreeRuntime, 0, len(s.runtimes)+len(s.retired))
	for _, runtime := range s.runtimes {
		if runtime != nil && !seen[runtime] {
			seen[runtime] = true
			runtimes = append(runtimes, runtime)
		}
	}
	for _, runtime := range s.retired {
		if runtime != nil && !seen[runtime] {
			seen[runtime] = true
			runtimes = append(runtimes, runtime)
		}
	}
	s.active = nil
	codebaseRuntime := s.codebaseRuntime
	s.mu.Unlock()
	if codebaseRuntime != nil {
		codebaseRuntime.BeginShutdown()
	}

	for _, runtime := range runtimes {
		if runtime.loop != nil {
			runtime.loop.Close()
		}
	}
	for _, runtime := range runtimes {
		if runtime.release != nil {
			runtime.release()
			runtime.release = nil
		}
	}
	for _, runtime := range runtimes {
		if runtime.registry != nil {
			runtime.registry.ShutdownAll()
		}
	}
	if codebaseRuntime != nil {
		retErr = errors.Join(retErr, codebaseRuntime.Close())
	}
	return retErr
}

func closeWorktreeRuntime(runtime *worktreeRuntime) {
	if runtime == nil {
		return
	}
	if runtime.loop != nil {
		runtime.loop.Close()
	}
	if runtime.release != nil {
		runtime.release()
		runtime.release = nil
	}
	if runtime.registry != nil {
		runtime.registry.ShutdownAll()
	}
}

func validateWorktreeRuntimePath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("worktree path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("open worktree %s: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("worktree path is not a directory: %s", path)
	}
	return nil
}
