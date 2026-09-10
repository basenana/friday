// Package actor provides a per-session Registry over the core/actor
// package (the AG-UI protocol actor). The Registry owns actor
// construction (wiring setup.NewAgent), event-stream subscription,
// idle eviction, and shutdown.
package actor

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/bus"
	"github.com/basenana/friday/config"
	coreactor "github.com/basenana/friday/core/actor"
	"github.com/basenana/friday/sessions"
	"github.com/basenana/friday/setup"
)

// RegistryConfig tunes the Registry.
type RegistryConfig struct {
	// IdleTimeout is how long an actor with no turn activity is kept
	// alive before being shut down and evicted. Default 5m.
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
// IdleTimeout of inactivity.
type Registry struct {
	mu      sync.Mutex
	entries map[string]*managedActor

	cfg     RegistryConfig
	sessMgr setup.SessionManager
	appCfg  *config.Config
	workdir string
	bus     *eventbus.Bus

	ctx    context.Context
	cancel context.CancelFunc
}

// NewRegistry creates a Registry and starts its idle-sweep loop.
func NewRegistry(sessMgr setup.SessionManager, appCfg *config.Config, cfg RegistryConfig) *Registry {
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
	r := &Registry{
		entries: make(map[string]*managedActor),
		cfg:     cfg,
		sessMgr: sessMgr,
		appCfg:  appCfg,
		workdir: workdirOrPWD(),
		bus:     cfg.Bus,
		ctx:     ctx,
		cancel:  cancel,
	}
	go r.sweepLoop()
	return r
}

// Bus returns the event bus the registry bridges its actors onto.
// Consumers (TUI, a2a, tests) subscribe and publish here instead of
// calling actor methods directly.
func (r *Registry) Bus() *eventbus.Bus { return r.bus }

// GetOrCreate returns the live Actor for sessionID, constructing it
// (agent + session via setup.NewAgent) on first use. An actor is built
// once for its lifetime; its session keeps in-memory history across
// turns, with persistence handled by the session store.
func (r *Registry) GetOrCreate(sessionID string) (*coreactor.Actor, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.entries[sessionID]; ok && !e.stopped.Load() {
		return e.actor, nil
	}
	if old, ok := r.entries[sessionID]; ok {
		// Stale entry from a concurrent shutdown; clean it up.
		delete(r.entries, sessionID)
		old.close(r.cfg.ShutdownGrace)
	}

	agentCtx, err := setup.NewAgent(r.sessMgr, r.appCfg, setup.WithSessionID(sessionID))
	if err != nil {
		return nil, fmt.Errorf("setup agent for session %s: %w", sessionID, err)
	}

	e := &managedActor{agentCtx: agentCtx}
	e.lastActive.Store(time.Now().UnixNano())

	opts := []coreactor.Option{coreactor.WithTurnLifecycle(e)}
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
	if evicted {
		bus.PublishStatus(r.bus, entry.sessionID, actorID, bus.StatusEvicted)
	}
	entry.managed.close(r.cfg.ShutdownGrace)
	bus.PublishStatus(r.bus, entry.sessionID, actorID, bus.StatusStopped)
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
	for _, entry := range entries {
		r.stopEntry(entry, false)
	}
}

// sweepLoop evicts actors whose last turn activity is older than
// IdleTimeout.
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
		if e.stopped.Load() || now.Sub(time.Unix(0, e.lastActive.Load())) > r.cfg.IdleTimeout {
			// Detach the exact instance while still holding the same lock used
			// for the staleness decision. A replacement created later under the
			// same session ID must never be targeted by this sweep.
			delete(r.entries, id)
			stale = append(stale, registryEntry{sessionID: id, managed: e})
		}
	}
	return stale
}

func workdirOrPWD() string {
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}
