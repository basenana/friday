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

	"github.com/basenana/friday/config"
	coreactor "github.com/basenana/friday/core/actor"
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
	ctx, cancel := context.WithCancel(context.Background())
	r := &Registry{
		entries: make(map[string]*managedActor),
		cfg:     cfg,
		sessMgr: sessMgr,
		appCfg:  appCfg,
		workdir: workdirOrPWD(),
		ctx:     ctx,
		cancel:  cancel,
	}
	go r.sweepLoop()
	return r
}

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
	r.entries[sessionID] = e
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
// sessionID. Errors when no live actor exists. The subscription's
// Events channel closes on Subscription.Close() or actor shutdown.
func (r *Registry) Subscribe(sessionID string) (*coreactor.Subscription, error) {
	r.mu.Lock()
	e, ok := r.entries[sessionID]
	r.mu.Unlock()
	if !ok || e.stopped.Load() {
		return nil, fmt.Errorf("actor session %q not found", sessionID)
	}
	return e.actor.Subscribe(), nil
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
		e.close(r.cfg.ShutdownGrace)
	}
}

// ShutdownAll stops every actor and the sweep loop.
func (r *Registry) ShutdownAll() {
	r.cancel() // stops the sweep loop and all actor loop contexts
	r.mu.Lock()
	entries := make([]*managedActor, 0, len(r.entries))
	for id, e := range r.entries {
		entries = append(entries, e)
		delete(r.entries, id)
	}
	r.mu.Unlock()
	for _, e := range entries {
		e.close(r.cfg.ShutdownGrace)
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
	now := time.Now()
	r.mu.Lock()
	var stale []string
	for id, e := range r.entries {
		if e.stopped.Load() || now.Sub(time.Unix(0, e.lastActive.Load())) > r.cfg.IdleTimeout {
			stale = append(stale, id)
		}
	}
	r.mu.Unlock()
	for _, id := range stale {
		r.Shutdown(id)
	}
}

func workdirOrPWD() string {
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}
