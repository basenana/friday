package a2a

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/a2asrv"
	"github.com/a2aproject/a2a-go/a2asrv/eventqueue"
	eventbus "github.com/hyponet/eventbus/bus"

	"github.com/basenana/friday/actor"
	"github.com/basenana/friday/bus"
	"github.com/basenana/friday/config"
	coreactor "github.com/basenana/friday/core/actor"
	"github.com/basenana/friday/core/actor/events"
	"github.com/basenana/friday/setup"
)

const defaultExecuteTimeout = 10 * time.Minute

// actorRegistry is the subset of *actor.Registry the executor depends on.
type actorRegistry interface {
	GetOrCreate(sessionID string) (*coreactor.Actor, error)
	Bus() *eventbus.Bus
	Shutdown(sessionID string)
	ShutdownAll()
}

type registryAdapter struct {
	inner *actor.Registry
}

func (r registryAdapter) GetOrCreate(sessionID string) (*coreactor.Actor, error) {
	return r.inner.GetOrCreate(sessionID)
}

func (r registryAdapter) Bus() *eventbus.Bus {
	return r.inner.Bus()
}

func (r registryAdapter) Shutdown(sessionID string) {
	r.inner.Shutdown(sessionID)
}

func (r registryAdapter) ShutdownAll() {
	r.inner.ShutdownAll()
}

// Server is the A2A HTTP server exposing Friday's chat capability.
type Server struct {
	cfg        Config
	registry   actorRegistry
	handler    a2asrv.RequestHandler
	httpServer *http.Server
	authToken  string
}

// NewRegistry builds the actor registry used by the A2A adapter.
func NewRegistry(fridayCfg *config.Config, sessMgr setup.SessionManager) *actor.Registry {
	return actor.NewRegistry(sessMgr, fridayCfg, actor.DefaultRegistryConfig())
}

// NewServer creates a new A2A server backed by an actor Registry.
func NewServer(cfg Config, registry *actor.Registry, authToken string) (*Server, error) {
	adapted := registryAdapter{inner: registry}
	executor := newFridayExecutor(adapted)
	handler := a2asrv.NewHandler(executor, a2asrv.WithLogger(slog.Default()))

	return &Server{
		cfg:       cfg,
		registry:  adapted,
		handler:   handler,
		authToken: authToken,
	}, nil
}

// Start starts the A2A HTTP server. Blocks until the server exits.
func (s *Server) Start() error {
	card := NewAgentCard(s.cfg)
	mux := http.NewServeMux()
	mux.Handle("/", a2asrv.NewJSONRPCHandler(s.handler))
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))

	handler := http.Handler(mux)
	if s.authToken != "" {
		handler = authMiddleware(s.authToken, mux)
	}

	s.httpServer = &http.Server{
		Handler: handler,
	}

	listener, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.cfg.Listen, err)
	}

	slog.Info("channel listening", "addr", "http://"+s.cfg.Listen)
	slog.Info("agent card available", "url", s.cfg.BaseURL+".well-known/agent-card.json")

	return s.httpServer.Serve(listener)
}

// Shutdown gracefully shuts down the A2A server and tears down all actors.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.registry != nil {
		s.registry.ShutdownAll()
	}
	if s.httpServer == nil {
		return nil
	}
	return s.httpServer.Shutdown(ctx)
}

// authMiddleware returns an HTTP middleware that validates Bearer tokens.
func authMiddleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] != token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// fridayExecutor implements a2asrv.AgentExecutor by routing requests through
// the actor Registry's topic bus. Each task is dispatched to a per-taskID
// actor; the session's ordered bus feed is translated into A2A queue events.
type fridayExecutor struct {
	registry actorRegistry
}

var _ a2asrv.AgentExecutor = (*fridayExecutor)(nil)

func newFridayExecutor(registry actorRegistry) *fridayExecutor {
	return &fridayExecutor{registry: registry}
}

// Execute dispatches the user message to the actor for this task and
// translates the resulting event stream into A2A queue events.
func (e *fridayExecutor) Execute(ctx context.Context, reqCtx *a2asrv.RequestContext, queue eventqueue.Queue) error {
	ctx, cancel := context.WithTimeout(ctx, defaultExecuteTimeout)
	defer cancel()

	if e.registry == nil {
		return writeTerminalState(ctx, queue, reqCtx, a2a.TaskStateFailed, errorMessage("actor registry unavailable"))
	}

	userText := extractTextFromMessage(reqCtx.Message)
	if userText == "" {
		userText = "(empty message)"
	}

	// A2A TaskID is the actor session id: one actor per task.
	sessionID := string(reqCtx.TaskID)
	// Subscribe before creation so status.created is the observable epoch
	// boundary and no early actor event can race the feed.
	feed := bus.SubscribeAgentFeed(e.registry.Bus(), sessionID)
	defer feed.Close()
	if _, err := e.registry.GetOrCreate(sessionID); err != nil {
		return writeTerminalState(ctx, queue, reqCtx, a2a.TaskStateFailed, errorMessage(fmt.Sprintf("actor setup failed: %v", err)))
	}
	defer e.registry.Shutdown(sessionID)

	// Emit submitted -> working state transitions.
	if reqCtx.StoredTask == nil {
		if err := queue.Write(ctx, a2a.NewStatusUpdateEvent(reqCtx, a2a.TaskStateSubmitted, nil)); err != nil {
			return fmt.Errorf("failed to write submitted event: %w", err)
		}
	}
	if err := queue.Write(ctx, a2a.NewStatusUpdateEvent(reqCtx, a2a.TaskStateWorking, nil)); err != nil {
		return fmt.Errorf("failed to write working event: %w", err)
	}

	// Send the message to the actor inbox over the bus. Delivery failures
	// come back asynchronously as status.inbox_dropped envelopes matched on
	// TurnID below.
	e.registry.Bus().Publish(bus.TopicInbox(sessionID),
		bus.NewUserInput(sessionID, "a2a", bus.UserTextInput{Text: userText, TurnID: string(reqCtx.TaskID)}))

	var (
		textBuf    strings.Builder
		artifactID a2a.ArtifactID
		runErr     string
	)

	for {
		select {
		case evt := <-feed.Events():
			switch evt.Type {
			case events.KindTextMessageContent:
				var d events.TextMessageContentData
				if events.DecodePayload(evt, &d) != nil {
					continue
				}
				delta := d.Content
				if strings.TrimSpace(delta) == "" && textBuf.Len() == 0 {
					continue
				}
				textBuf.WriteString(delta)

				part := a2a.TextPart{Text: delta}
				var event *a2a.TaskArtifactUpdateEvent
				if artifactID == "" {
					event = a2a.NewArtifactEvent(reqCtx, part)
					artifactID = event.Artifact.ID
				} else {
					event = a2a.NewArtifactUpdateEvent(reqCtx, artifactID, part)
				}
				if err := queue.Write(ctx, event); err != nil {
					return fmt.Errorf("failed to write artifact event: %w", err)
				}

			case events.KindRunError:
				msg := "run failed"
				var d events.RunErrorData
				if events.DecodePayload(evt, &d) == nil && d.Message != "" {
					msg = d.Message
				}
				runErr = msg

			case events.KindRunFinished:
				return writeTaskTerminalState(ctx, queue, reqCtx, evt, runErr, textBuf.String())

			case events.KindCustom:
				if evt.Name != "status."+bus.StatusInboxDropped {
					continue
				}
				var drop bus.InboxDropped
				if events.DecodePayload(evt, &drop) != nil {
					continue
				}
				// Only fail on drops of this task's input; other turns'
				// drops are not ours to report.
				if drop.TurnID == string(reqCtx.TaskID) {
					return writeTerminalState(ctx, queue, reqCtx, a2a.TaskStateFailed, errorMessage("actor inbox dropped: "+drop.Reason))
				}
			}

		case <-ctx.Done():
			return writeTerminalState(ctx, queue, reqCtx, a2a.TaskStateCanceled, nil)
		}
	}
}

// Cancel handles task cancellation by preempting the actor's in-flight
// turn for the task over the bus. The terminal Canceled status is written
// by Execute's event loop when it observes the resulting RUN_FINISHED;
// here we only record the cancel request.
func (e *fridayExecutor) Cancel(ctx context.Context, reqCtx *a2asrv.RequestContext, queue eventqueue.Queue) error {
	if e.registry != nil {
		tid := string(reqCtx.TaskID)
		e.registry.Bus().Publish(bus.TopicPreempt(tid), bus.NewPreempt(tid, "a2a", "a2a tasks/cancel"))
	}
	return queue.Write(ctx, a2a.NewStatusUpdateEvent(reqCtx, a2a.TaskStateCanceled, nil))
}
