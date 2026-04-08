package a2a

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/a2asrv"
	"github.com/a2aproject/a2a-go/a2asrv/eventqueue"
	"github.com/basenana/friday/config"
	"github.com/basenana/friday/setup"
)

const defaultExecuteTimeout = 10 * time.Minute

// Server is the A2A HTTP server exposing Friday's chat capability.
type Server struct {
	cfg        Config
	fridayCfg  *config.Config
	sessMgr    setup.SessionManager
	handler    a2asrv.RequestHandler
	httpServer *http.Server
	authToken  string
}

// NewServer creates a new A2A server.
func NewServer(cfg Config, fridayCfg *config.Config, sessMgr setup.SessionManager, authToken string) (*Server, error) {
	executor := newFridayExecutor(sessMgr, fridayCfg)
	handler := a2asrv.NewHandler(executor, a2asrv.WithLogger(slog.Default()))

	return &Server{
		cfg:       cfg,
		fridayCfg: fridayCfg,
		sessMgr:   sessMgr,
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

// Shutdown gracefully shuts down the A2A server.
func (s *Server) Shutdown(ctx context.Context) error {
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

// fridayExecutor implements a2asrv.AgentExecutor by bridging Friday's chat to A2A events.
type fridayExecutor struct {
	sessMgr   setup.SessionManager
	fridayCfg *config.Config
	cancels   sync.Map // taskID → context.CancelFunc
}

var _ a2asrv.AgentExecutor = (*fridayExecutor)(nil)

func newFridayExecutor(sessMgr setup.SessionManager, fridayCfg *config.Config) *fridayExecutor {
	return &fridayExecutor{
		sessMgr:   sessMgr,
		fridayCfg: fridayCfg,
	}
}

// Execute runs Friday's chat and writes A2A events to the queue.
func (e *fridayExecutor) Execute(ctx context.Context, reqCtx *a2asrv.RequestContext, queue eventqueue.Queue) error {
	// Wrap context with timeout and register cancel func for Cancel()
	ctx, cancel := context.WithTimeout(ctx, defaultExecuteTimeout)
	e.cancels.Store(reqCtx.TaskID, cancel)
	defer func() {
		cancel()
		e.cancels.Delete(reqCtx.TaskID)
	}()

	// Extract user message text from the A2A message
	userText := extractTextFromMessage(reqCtx.Message)
	if userText == "" {
		userText = "(empty message)"
	}

	// Emit submitted -> working state transitions
	if reqCtx.StoredTask == nil {
		if err := queue.Write(ctx, a2a.NewStatusUpdateEvent(reqCtx, a2a.TaskStateSubmitted, nil)); err != nil {
			return fmt.Errorf("failed to write submitted event: %w", err)
		}
	}

	if err := queue.Write(ctx, a2a.NewStatusUpdateEvent(reqCtx, a2a.TaskStateWorking, nil)); err != nil {
		return fmt.Errorf("failed to write working event: %w", err)
	}

	// Create a fresh Friday agent for this request
	agentCtx, err := setup.NewAgent(e.sessMgr, e.fridayCfg)
	if err != nil {
		_ = queue.Write(ctx, a2a.NewStatusUpdateEvent(reqCtx, a2a.TaskStateFailed, errorMessage(err.Error())))
		return nil
	}
	defer agentCtx.TaskManager.KillAll()

	// Run Friday chat
	resp := agentCtx.Chat(ctx, userText)

	// Bridge Friday's streaming response to A2A events
	return bridgeResponse(ctx, reqCtx, queue, resp)
}

// Cancel handles task cancellation requests.
func (e *fridayExecutor) Cancel(ctx context.Context, reqCtx *a2asrv.RequestContext, queue eventqueue.Queue) error {
	if val, ok := e.cancels.LoadAndDelete(reqCtx.TaskID); ok {
		val.(context.CancelFunc)()
	}
	return queue.Write(ctx, a2a.NewStatusUpdateEvent(reqCtx, a2a.TaskStateCanceled, nil))
}
