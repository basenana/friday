package actor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/setup"
)

// RegistryConfig tunes the Registry.
type RegistryConfig struct {
	// IdleTimeout is how long an Idle actor is kept alive before being shut
	// down and evicted. Default 5m.
	IdleTimeout time.Duration
	// SweepInterval is the idle-sweep ticker period. Default 30s.
	SweepInterval time.Duration
	// InboxBuffer is the buffer size for newly-created actor inboxes.
	InboxBuffer int
	// OutcomeBuffer is the buffer size for newly-created actor outcomes.
	OutcomeBuffer int
	// OnEvent, if non-nil, is invoked for every event emitted by every
	// actor managed by this registry. The registry guarantees one fanout
	// goroutine per actor (i.e. exactly-once delivery to this callback)
	// before forwarding events to API-layer subscribers. Leaving this nil
	// means events are only delivered to direct subscribers.
	OnEvent func(sessionID string, evt Event)
}

// DefaultRegistryConfig returns a sensible default configuration.
func DefaultRegistryConfig() RegistryConfig {
	return RegistryConfig{
		IdleTimeout:   5 * time.Minute,
		SweepInterval: 30 * time.Second,
		InboxBuffer:   16,
		OutcomeBuffer: 256,
	}
}

// Registry owns the set of live Actors and supervises their lifecycle
// (creation, lookup, idle eviction, graceful shutdown at process exit).
type Registry struct {
	mu      sync.RWMutex
	actors  map[string]*Actor
	streams map[string]*sessionPubSub
	cfg     RegistryConfig
	sessMgr setup.SessionManager
	appCfg  *config.Config

	ctx    context.Context
	cancel context.CancelFunc
}

// NewRegistry creates a Registry and starts its idle-sweep goroutine.
func NewRegistry(sessMgr setup.SessionManager, appCfg *config.Config, cfg RegistryConfig) *Registry {
	ctx, cancel := context.WithCancel(context.Background())
	r := &Registry{
		actors:  make(map[string]*Actor),
		streams: make(map[string]*sessionPubSub),
		cfg:     cfg,
		sessMgr: sessMgr,
		appCfg:  appCfg,
		ctx:     ctx,
		cancel:  cancel,
	}
	go r.sweepLoop()
	return r
}

// GetOrCreate returns the existing live actor for sessionID, or builds a new
// one. If a previously-shutdown actor is still in the map it is evicted and
// replaced. Exactly one fanout pump goroutine is started per actor.
func (r *Registry) GetOrCreate(sessionID string) *Actor {
	r.mu.RLock()
	a, exists := r.actors[sessionID]
	r.mu.RUnlock()

	if exists && a.State() != StateShutdown {
		return a
	}

	r.mu.Lock()
	// double-check under write lock
	if a, exists = r.actors[sessionID]; exists && a.State() != StateShutdown {
		r.mu.Unlock()
		return a
	}
	delete(r.actors, sessionID)
	delete(r.streams, sessionID)

	a = New(sessionID, r.sessMgr, r.appCfg,
		WithInboxBuffer(r.cfg.InboxBuffer),
		WithOutcomeBuffer(r.cfg.OutcomeBuffer),
	)
	stream := newSessionPubSub()
	r.actors[sessionID] = a
	r.streams[sessionID] = stream
	r.mu.Unlock()

	// Start the single pump goroutine for this actor. Started outside the
	// lock so Shutdown→evict cannot observe a half-initialised pump.
	go r.fanout(a, stream)
	return a
}

// Get looks up an actor without creating one.
func (r *Registry) Get(sessionID string) (*Actor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.actors[sessionID]
	if !ok || a.State() == StateShutdown {
		return nil, false
	}
	return a, true
}

// Subscribe registers an API-layer subscriber for one actor session.
// The returned unsubscribe function is idempotent.
func (r *Registry) Subscribe(sessionID string, buffer int) (<-chan Event, func(), error) {
	r.mu.RLock()
	stream, ok := r.streams[sessionID]
	r.mu.RUnlock()

	if !ok {
		return nil, nil, fmt.Errorf("actor session %q not found", sessionID)
	}

	ch, unsubscribe := stream.subscribe(buffer)
	return ch, unsubscribe, nil
}

// Shutdown evicts an actor from the registry and gracefully shuts it down.
func (r *Registry) Shutdown(sessionID string) {
	r.mu.Lock()
	a, ok := r.actors[sessionID]
	stream, streamOK := r.streams[sessionID]
	if ok {
		delete(r.actors, sessionID)
	}
	if streamOK {
		delete(r.streams, sessionID)
	}
	r.mu.Unlock()

	if ok {
		a.Shutdown()
	}
	if streamOK {
		stream.stop()
	}
}

// ShutdownAll evicts everything; intended for process exit. Stops the sweep
// loop and waits for each actor's loop goroutine to exit.
func (r *Registry) ShutdownAll() {
	r.cancel()

	r.mu.Lock()
	actors := make([]*Actor, 0, len(r.actors))
	streams := make([]*sessionPubSub, 0, len(r.streams))
	for _, a := range r.actors {
		actors = append(actors, a)
	}
	for _, stream := range r.streams {
		streams = append(streams, stream)
	}
	r.actors = make(map[string]*Actor)
	r.streams = make(map[string]*sessionPubSub)
	r.mu.Unlock()

	for _, a := range actors {
		a.Shutdown()
	}
	for _, stream := range streams {
		stream.stop()
	}
}

// fanout is the single pump goroutine per actor. It reads events from the
// outcome channel and dispatches them through the registry-level OnEvent
// callback. Exits automatically when the outcome channel is closed.
func (r *Registry) fanout(a *Actor, stream *sessionPubSub) {
	stream.run(a.Outcome(), func(evt Event) {
		if r.cfg.OnEvent != nil {
			r.cfg.OnEvent(a.SessionID, evt)
		}
	})
}

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

	r.mu.RLock()
	var toRemove []string
	for id, a := range r.actors {
		if a.State() == StateShutdown {
			toRemove = append(toRemove, id)
			continue
		}
		if a.State() == StateIdle && now.Sub(a.LastActive()) > r.cfg.IdleTimeout {
			toRemove = append(toRemove, id)
		}
	}
	r.mu.RUnlock()

	for _, id := range toRemove {
		r.Shutdown(id)
	}
}
