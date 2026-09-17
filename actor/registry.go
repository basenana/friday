// Package actor provides a per-session Registry over the core/actor
// package (the AG-UI protocol actor). The Registry owns actor
// construction (wiring setup.NewAgent), event-stream subscription,
// idle eviction, and shutdown.
package actor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/bus"
	coderagents "github.com/basenana/friday/coder/agents"
	"github.com/basenana/friday/config"
	coreactor "github.com/basenana/friday/core/actor"
	"github.com/basenana/friday/core/collaboration"
	"github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/core/planning"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/providers/fallback"
	coresession "github.com/basenana/friday/core/session"
	fridaymcp "github.com/basenana/friday/mcp"
	"github.com/basenana/friday/sandbox"
	"github.com/basenana/friday/sessions"
	"github.com/basenana/friday/setup"
	"github.com/basenana/friday/skills"
	"github.com/basenana/friday/workspace"
)

// RegistryConfig tunes the Registry.
type RegistryConfig struct {
	// Catalog opens root sessions for actors. When nil, Registry preserves the
	// legacy setup path, including its get-or-create behavior.
	Catalog sessions.RootCatalog
	// Workdir is the canonical runtime root used by file validation and agent
	// tools. Empty preserves the legacy cwd fallback.
	Workdir string
	// IdleTimeout is how long an actor with no activity is kept alive before
	// being shut down and evicted. Active turns, lifecycle leases, and running
	// background tasks are never considered idle. Default 5m.
	IdleTimeout time.Duration
	// SweepInterval is the idle-sweep ticker period. Default 30s.
	SweepInterval time.Duration
	// InboxBuffer is the inbox buffer size for newly-created actors.
	// Zero means the core/actor default.
	InboxBuffer int
	// ShutdownGrace is the graceful-shutdown budget given to an
	// in-flight turn before it is force-aborted during eviction.
	// Default 2s.
	ShutdownGrace time.Duration
	// FilePathValidator, when non-nil, overrides the default file-card
	// path validator (workdir-confined, see paths.go).
	FilePathValidator coreactor.FilePathValidator
	// Bus is the event bus actors bridge onto. When nil, the registry
	// creates a private bus; share one bus across registries (or pass
	// the registry's own Bus()) when consumers need to observe actors.
	Bus *eventbus.Bus
	// AgentPlanEntry exposes enter_plan_mode to the agent. It should only be
	// enabled by interactive clients that implement plan approval.
	AgentPlanEntry bool
	// ConfigTools exposes agent_config and mcp_config to every agent spawned by
	// this registry. Interactive coder clients enable it explicitly.
	ConfigTools bool
}

// DefaultRegistryConfig returns a sensible default configuration.
func DefaultRegistryConfig() RegistryConfig {
	return RegistryConfig{
		IdleTimeout:   5 * time.Minute,
		SweepInterval: 30 * time.Second,
		ShutdownGrace: 2 * time.Second,
	}
}

// Registry manages one core/actor Actor per session, along with the
// setup.AgentContext backing it. Actors are created lazily via
// GetOrCreate and torn down on Shutdown, ShutdownAll, or after
// IdleTimeout of inactivity, provided they own no protected work.
type Registry struct {
	mu      sync.Mutex
	entries map[string]*managedActor

	cfg     RegistryConfig
	sessMgr setup.SessionManager
	appCfg  *config.Config
	catalog sessions.RootCatalog
	workdir string
	bus     *eventbus.Bus
	skills  *skills.Registry
	agents  *coderagents.Registry
	models  *fallback.ModelPool
	mcp     *fridaymcp.Manager

	ctx    context.Context
	cancel context.CancelFunc
}

// NewRegistry creates a Registry and starts its idle-sweep loop.
func NewRegistry(sessMgr setup.SessionManager, appCfg *config.Config, cfg RegistryConfig) (*Registry, error) {
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 5 * time.Minute
	}
	if cfg.SweepInterval <= 0 {
		cfg.SweepInterval = 30 * time.Second
	}
	if cfg.ShutdownGrace <= 0 {
		cfg.ShutdownGrace = 2 * time.Second
	}
	if cfg.Bus == nil {
		cfg.Bus = eventbus.NewBus()
	}
	ctx, cancel := context.WithCancel(context.Background())
	catalog := cfg.Catalog
	workdir := strings.TrimSpace(cfg.Workdir)
	if workdir == "" {
		workdir = workdirOrPWD()
	}
	ws := workspace.NewFromConfig(appCfg)
	skillLoader := skills.NewLoader(ws.SkillsPaths()...)
	if err := skillLoader.Load(); err != nil {
		logger.New("actor.registry").Warnw("failed to load skills", "error", err)
	}
	agentRegistry, err := coderagents.NewLoader(appCfg.AgentPaths()...).Load()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("load agents: %w", err)
	}
	if err := agentRegistry.SetValidator(func(spec *coderagents.AgentSpec) error {
		if spec != nil && spec.Model != "" && !appCfg.HasModelName(spec.Model) {
			return fmt.Errorf("agent %q in %s selects unknown model %q", spec.Name, spec.SourcePath, spec.Model)
		}
		return nil
	}); err != nil {
		cancel()
		return nil, err
	}
	modelPool, err := setup.CreateModelPool(appCfg)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create model pool: %w", err)
	}
	r := &Registry{
		entries: make(map[string]*managedActor),
		cfg:     cfg,
		sessMgr: sessMgr,
		appCfg:  appCfg,
		catalog: catalog,
		workdir: workdir,
		bus:     cfg.Bus,
		skills:  skills.NewRegistry(skillLoader),
		agents:  agentRegistry,
		models:  modelPool,
		ctx:     ctx,
		cancel:  cancel,
	}
	mcpManager, err := fridaymcp.NewManager(fridaymcp.ManagerConfig{
		ConfigRoots: ws.MCPRoots(), ProjectRoot: workdir,
		CacheRoot: appCfg.CachesPath(), TrustPath: filepath.Join(appCfg.StatePath(), "mcp_trust.json"),
	})
	if err != nil {
		logger.New("actor.registry").Warnw("failed to load MCP configuration", "error", err)
	} else {
		r.mcp = mcpManager
		go r.mcp.Warmup(r.ctx)
	}
	go r.sweepLoop()
	return r, nil
}

// Bus returns the event bus the registry bridges its actors onto.
// Consumers (TUI, daemon, tests) subscribe and publish here instead of
// calling actor methods directly.
func (r *Registry) Bus() *eventbus.Bus { return r.bus }

// SkillRegistry returns the shared, refreshable skills catalog used by all
// agents owned by this registry.
func (r *Registry) SkillRegistry() *skills.Registry { return r.skills }

// AgentRegistry returns the shared, mtime-refreshable catalog of disk-defined agents.
func (r *Registry) AgentRegistry() *coderagents.Registry { return r.agents }

// MCPManager returns the process-shared MCP manager used by every actor.
func (r *Registry) MCPManager() *fridaymcp.Manager { return r.mcp }

// GetOrCreate returns the live Actor for sessionID, constructing it
// (agent + session via setup.NewAgent) on first use. An actor is built
// once for its lifetime; its session keeps in-memory history across
// turns, with persistence handled by the session store.
func (r *Registry) GetOrCreate(sessionID string) (result *coreactor.Actor, resultErr error) {
	// Cache misses finish their bounded handshake before the first actor is
	// exposed. Cached schemas remain non-blocking while their connection is
	// refreshed in the background.
	if r.mcp != nil {
		r.mcp.Warmup(r.ctx)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[sessionID]; ok && !e.stopped.Load() {
		e.touch()
		return e.actor, nil
	}
	if old, ok := r.entries[sessionID]; ok {
		// Stale entry from a concurrent shutdown; clean it up.
		delete(r.entries, sessionID)
		old.close(r.cfg.ShutdownGrace)
	}
	started := time.Now()
	registryLogger := logger.New("actor.registry")
	registryLogger.Infow("creating session actor",
		"session_id", sessionID,
		"project_mode", r.catalog != nil,
	)
	defer func() {
		fields := []interface{}{
			"session_id", sessionID,
			"project_mode", r.catalog != nil,
			"duration_ms", time.Since(started).Milliseconds(),
		}
		if resultErr != nil {
			fields = append(fields, "error", resultErr.Error())
			registryLogger.Errorw("session actor creation failed", fields...)
			return
		}
		if result != nil {
			fields = append(fields, "actor_id", result.ID())
		}
		registryLogger.Infow("session actor created", fields...)
	}()

	policy := fallback.NewSessionPolicy(r.sessionPolicy(sessionID))
	client := r.models.NewClient(policy, providers.ClientPolicy{})
	var agentCtx *setup.AgentContext
	var err error
	if r.catalog != nil {
		lifecycle, openErr := r.catalog.OpenRoot(r.ctx, sessionID, client, coresession.WithState(workspace.NewFileState(r.appCfg.StatePath())))
		if openErr != nil {
			return nil, fmt.Errorf("open session lifecycle %s: %w", sessionID, openErr)
		}
		agentCtx, err = setup.NewAgentWithLifecycle(lifecycle, r.sessMgr, r.appCfg,
			setup.WithProviderClient(client), setup.WithModelPool(r.models), setup.WithSessionPolicy(policy), setup.WithSkillRegistry(r.skills), setup.WithAgentRegistry(r.agents), setup.WithMCPManager(r.mcp), setup.WithWorkdir(r.workdir), setup.WithConfigTools(r.cfg.ConfigTools))
		if err != nil {
			_ = lifecycle.Close()
		}
	} else {
		agentCtx, err = setup.NewAgent(r.sessMgr, r.appCfg, setup.WithSessionID(sessionID),
			setup.WithProviderClient(client), setup.WithModelPool(r.models), setup.WithSessionPolicy(policy), setup.WithSkillRegistry(r.skills), setup.WithAgentRegistry(r.agents), setup.WithMCPManager(r.mcp), setup.WithWorkdir(r.workdir), setup.WithConfigTools(r.cfg.ConfigTools))
	}
	if err != nil {
		return nil, fmt.Errorf("setup agent for session %s: %w", sessionID, err)
	}

	e := &managedActor{agentCtx: agentCtx}
	e.lastActive.Store(time.Now().UnixNano())

	opts := []coreactor.Option{coreactor.WithTurnLifecycle(e)}
	if modes, ok := r.sessMgr.(interface {
		CollaborationMode(string) collaboration.Mode
	}); ok {
		if provider, ok := r.sessMgr.(interface{ GetStore() sessions.Store }); ok {
			if plans, ok := provider.GetStore().(planning.Repository); ok {
				opts = append(opts, coreactor.WithPlanning(modes, plans))
				if r.cfg.AgentPlanEntry {
					opts = append(opts, coreactor.WithAgentPlanEntry(true))
				}
			}
		}
	}
	if provider, ok := r.sessMgr.(interface{ GetStore() sessions.Store }); ok {
		if eventStore, ok := provider.GetStore().(sessions.EventStore); ok {
			eventSink, sinkErr := eventStore.OpenEventSink(r.ctx, sessionID)
			if sinkErr != nil {
				agentCtx.Close()
				return nil, fmt.Errorf("open event log for session %s: %w", sessionID, sinkErr)
			}
			opts = append(opts, coreactor.WithSink(eventSink))
		}
	}
	if r.cfg.InboxBuffer > 0 {
		opts = append(opts, coreactor.WithInboxBuffer(r.cfg.InboxBuffer))
	}
	validator := r.cfg.FilePathValidator
	if validator == nil {
		validator = newFilePathValidator(r.workdir)
	}
	opts = append(opts, coreactor.WithFilePathValidator(validator))

	e.actor = coreactor.New(agentCtx.Agent, agentCtx.Session, opts...)
	e.stopLoop = e.actor.Start(r.ctx) // loop tied to registry lifetime
	e.attach(r.bus, sessionID)
	r.entries[sessionID] = e
	bus.PublishStatus(r.bus, sessionID, e.actor.ID(), bus.StatusCreated)
	return e.actor, nil
}

// AcquireLifecycle returns a live root-bound lifecycle and pins its actor
// against idle eviction until the returned release function is called.
// Explicit Shutdown still takes precedence over a lease.
func (r *Registry) AcquireLifecycle(sessionID string) (sessions.SessionLifecycle, func(), error) {
	if _, err := r.GetOrCreate(sessionID); err != nil {
		return nil, nil, err
	}
	r.mu.Lock()
	e, ok := r.entries[sessionID]
	if !ok || e.stopped.Load() || e.agentCtx == nil || e.agentCtx.Lifecycle == nil {
		r.mu.Unlock()
		return nil, nil, fmt.Errorf("session actor %q is not running", sessionID)
	}
	e.acquire()
	lifecycle := e.agentCtx.Lifecycle
	r.mu.Unlock()

	var once sync.Once
	release := func() { once.Do(e.release) }
	return lifecycle, release, nil
}

// DispatchInput delivers one actor inbox envelope. User text transparently
// starts an evicted actor; stateful controls require the original live actor
// because pending forms and cancellation targets cannot be reconstructed.
func (r *Registry) DispatchInput(env bus.Envelope) error {
	if strings.TrimSpace(env.Session) == "" {
		return errors.New("input session is required")
	}
	if env.Name == bus.InboxUserText {
		if _, err := r.GetOrCreate(env.Session); err != nil {
			return err
		}
	}
	e, release, err := r.acquireExisting(env.Session)
	if err != nil {
		return err
	}
	defer release()
	if e.stopped.Load() {
		return fmt.Errorf("session actor %q is not running", env.Session)
	}
	env.Topic = bus.TopicInbox(env.Session)
	r.bus.Publish(env.Topic, env)
	return nil
}

// DispatchPreempt delivers a preemption only to an existing actor. Starting a
// replacement cannot cancel work owned by the previous actor instance.
func (r *Registry) DispatchPreempt(env bus.Envelope) error {
	if strings.TrimSpace(env.Session) == "" {
		return errors.New("preempt session is required")
	}
	e, release, err := r.acquireExisting(env.Session)
	if err != nil {
		return err
	}
	defer release()
	if e.stopped.Load() {
		return fmt.Errorf("session actor %q is not running", env.Session)
	}
	env.Topic = bus.TopicPreempt(env.Session)
	r.bus.Publish(env.Topic, env)
	return nil
}

func (r *Registry) acquireExisting(sessionID string) (*managedActor, func(), error) {
	r.mu.Lock()
	e, ok := r.entries[sessionID]
	if !ok || e.stopped.Load() {
		r.mu.Unlock()
		return nil, nil, fmt.Errorf("session actor %q is not running", sessionID)
	}
	e.acquire()
	r.mu.Unlock()

	var once sync.Once
	return e, func() { once.Do(e.release) }, nil
}

func (r *Registry) sessionPolicy(sessionID string) providers.ClientPolicy {
	var policy providers.ClientPolicy
	provider, ok := r.sessMgr.(interface{ GetStore() sessions.Store })
	if !ok {
		return policy
	}
	meta, err := provider.GetStore().GetMeta(sessionID)
	if err != nil || meta == nil {
		return policy
	}
	if r.appCfg.HasModelName(meta.Runtime.Model.Model) {
		policy.PreferredModel = meta.Runtime.Model.Model
	}
	if providers.IsValidReasoningEffort(meta.Runtime.Effort) {
		policy.Effort = meta.Runtime.Effort
	}
	return policy
}

// RefreshSessionPolicy applies persisted model and effort choices to a live
// actor without rebuilding it. If the actor is not live, its next creation
// reads the persisted runtime and starts with the same policy.
func (r *Registry) RefreshSessionPolicy(sessionID string) {
	policy := r.sessionPolicy(sessionID)
	r.mu.Lock()
	entry := r.entries[sessionID]
	if entry != nil && !entry.stopped.Load() && entry.agentCtx != nil && entry.agentCtx.Policy != nil {
		entry.agentCtx.Policy.Update(policy)
	}
	r.mu.Unlock()
}

// SessionClientRuntime returns the primary Agent client's current routing
// snapshot. Agent and subagent forks own separate snapshots and therefore do
// not affect this Session-level view.
func (r *Registry) SessionClientRuntime(sessionID string) (providers.ClientRuntimeInfo, bool) {
	r.mu.Lock()
	entry := r.entries[sessionID]
	if entry == nil || entry.stopped.Load() || entry.agentCtx == nil {
		r.mu.Unlock()
		return providers.ClientRuntimeInfo{}, false
	}
	client := entry.agentCtx.Client
	r.mu.Unlock()
	return providers.RuntimeInfo(client)
}

func (r *Registry) ListTasks(sessionID string) []*sandbox.Task {
	r.mu.Lock()
	entry := r.entries[sessionID]
	r.mu.Unlock()
	if entry == nil || entry.stopped.Load() || entry.agentCtx == nil || entry.agentCtx.TaskManager == nil {
		return nil
	}
	return entry.agentCtx.TaskManager.List("")
}

func countRunningTasks(tasks []*sandbox.Task) int {
	count := 0
	for _, task := range tasks {
		if task != nil && task.Status == sandbox.TaskRunning {
			count++
		}
	}
	return count
}

func (r *Registry) KillTask(sessionID, taskID string) error {
	if _, err := r.GetOrCreate(sessionID); err != nil {
		return fmt.Errorf("restore session actor: %w", err)
	}
	entry, release, err := r.acquireExisting(sessionID)
	if err != nil {
		return err
	}
	defer release()
	if entry.agentCtx == nil || entry.agentCtx.TaskManager == nil {
		return fmt.Errorf("session actor is not running")
	}
	if taskID == "all" {
		entry.agentCtx.TaskManager.KillAll()
		return nil
	}
	resolved, err := resolveTaskID(entry.agentCtx.TaskManager.List(""), taskID)
	if err != nil {
		return err
	}
	return entry.agentCtx.TaskManager.Kill(resolved)
}

func resolveTaskID(tasks []*sandbox.Task, target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", fmt.Errorf("task id is required")
	}
	var matches []string
	for _, task := range tasks {
		if task.ID == target {
			return task.ID, nil
		}
		if strings.HasPrefix(task.ID, target) {
			matches = append(matches, task.ID)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("ambiguous task prefix %q", target)
	}
	return "", fmt.Errorf("task not found: %s", target)
}

// Get returns the live Actor for sessionID, if any.
func (r *Registry) Get(sessionID string) (*coreactor.Actor, bool) {
	r.mu.Lock()
	e, ok := r.entries[sessionID]
	r.mu.Unlock()
	if !ok || e.stopped.Load() {
		return nil, false
	}
	return e.actor, true
}

// Lifecycle returns the root-bound session capability held by a live actor.
// It intentionally does not expose the registry's global session manager.
func (r *Registry) Lifecycle(sessionID string) (sessions.SessionLifecycle, bool) {
	r.mu.Lock()
	e, ok := r.entries[sessionID]
	r.mu.Unlock()
	if !ok || e.stopped.Load() || e.agentCtx == nil || e.agentCtx.Lifecycle == nil {
		return nil, false
	}
	return e.agentCtx.Lifecycle, true
}

// Subscribe returns an event-stream subscription for the live actor of
// sessionID. Deprecated: new integrations should subscribe through
// bus.SubscribeAgentFeed(r.Bus(), sessionID). This compatibility method is
// retained so existing Registry consumers do not break during migration.
func (r *Registry) Subscribe(sessionID string) (*coreactor.Subscription, error) {
	r.mu.Lock()
	e, ok := r.entries[sessionID]
	r.mu.Unlock()
	if !ok || e.stopped.Load() {
		return nil, fmt.Errorf("actor session %q not found", sessionID)
	}
	return e.actor.Subscribe(), nil
}

type registryEntry struct {
	sessionID string
	managed   *managedActor
}

func (r *Registry) stopEntry(entry registryEntry, evicted bool) {
	actorID := entry.managed.actor.ID()
	started := time.Now()
	if evicted {
		bus.PublishStatus(r.bus, entry.sessionID, actorID, bus.StatusEvicted)
	}
	entry.managed.close(r.cfg.ShutdownGrace)
	bus.PublishStatus(r.bus, entry.sessionID, actorID, bus.StatusStopped)
	logger.New("actor.registry").Infow("session actor stopped",
		"session_id", entry.sessionID,
		"actor_id", actorID,
		"evicted", evicted,
		"duration_ms", time.Since(started).Milliseconds(),
	)
}

// Shutdown stops the actor for sessionID (gracefully: an in-flight turn
// gets ShutdownGrace to finish before being aborted) and releases its
// agent context. Idempotent; missing sessions are ignored.
func (r *Registry) Shutdown(sessionID string) {
	r.mu.Lock()
	e, ok := r.entries[sessionID]
	delete(r.entries, sessionID)
	r.mu.Unlock()
	if ok {
		r.stopEntry(registryEntry{sessionID: sessionID, managed: e}, false)
	}
}

// ShutdownAll stops every actor and the sweep loop.
func (r *Registry) ShutdownAll() {
	r.cancel() // stops the sweep loop and all actor loop contexts
	r.mu.Lock()
	entries := make([]registryEntry, 0, len(r.entries))
	for id, e := range r.entries {
		entries = append(entries, registryEntry{sessionID: id, managed: e})
		delete(r.entries, id)
	}
	r.mu.Unlock()
	logger.New("actor.registry").Infow("shutting down all session actors", "actor_count", len(entries))
	for _, entry := range entries {
		r.stopEntry(entry, false)
	}
	if r.mcp != nil {
		_ = r.mcp.Close()
	}
}

// sweepLoop evicts actors that have exceeded IdleTimeout and own no protected
// work.
func (r *Registry) sweepLoop() {
	ticker := time.NewTicker(r.cfg.SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			r.sweep()
		}
	}
}

func (r *Registry) sweep() {
	stale := r.detachStale(time.Now())
	for _, entry := range stale {
		r.stopEntry(entry, true)
	}
}

func (r *Registry) detachStale(now time.Time) []registryEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	var stale []registryEntry
	for id, e := range r.entries {
		if e.stopped.Load() {
			delete(r.entries, id)
			stale = append(stale, registryEntry{sessionID: id, managed: e})
			continue
		}
		if now.Sub(time.Unix(0, e.lastActive.Load())) <= r.cfg.IdleTimeout {
			continue
		}
		runningTasks := e.agentCtx != nil && e.agentCtx.TaskManager != nil && countRunningTasks(e.agentCtx.TaskManager.List("")) > 0
		if e.protected() || runningTasks {
			// Give completed turns, released leases, and finished background tasks
			// a full idle window before the next eligibility decision.
			e.touch()
			continue
		}
		// Detach the exact instance while still holding the same lock used
		// for the staleness decision. A replacement created later under the
		// same session ID must never be targeted by this sweep.
		delete(r.entries, id)
		stale = append(stale, registryEntry{sessionID: id, managed: e})
	}
	return stale
}

func workdirOrPWD() string {
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}
