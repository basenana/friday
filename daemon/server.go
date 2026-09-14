package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"sync"
	"time"

	eventbus "github.com/hyponet/eventbus/bus"

	rootactor "github.com/basenana/friday/actor"
	"github.com/basenana/friday/bus"
	coreactor "github.com/basenana/friday/core/actor"
	"github.com/basenana/friday/core/actor/events"
)

const defaultPort = 8999

var safeThreadID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

type ActorRegistry interface {
	GetOrCreate(string) (*coreactor.Actor, error)
	Bus() *eventbus.Bus
	ShutdownAll()
}

var _ ActorRegistry = (*rootactor.Registry)(nil)

type Config struct {
	Port int
}

type Server struct {
	cfg      Config
	registry ActorRegistry
	catalog  SessionCatalog

	ctx    context.Context
	cancel context.CancelFunc
	http   *http.Server

	seenMu sync.Mutex
	seen   map[string]map[string]struct{}
	active map[string]string
}

func NewServer(cfg Config, registry ActorRegistry, catalog SessionCatalog) (*Server, error) {
	if registry == nil {
		return nil, errors.New("actor registry is required")
	}
	if catalog == nil {
		return nil, errors.New("session catalog is required")
	}
	if cfg.Port == 0 {
		cfg.Port = defaultPort
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, fmt.Errorf("invalid daemon port %d", cfg.Port)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{cfg: cfg, registry: registry, catalog: catalog, ctx: ctx, cancel: cancel,
		seen: make(map[string]map[string]struct{}), active: make(map[string]string)}
	s.http = &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	return s, nil
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws" {
			http.NotFound(w, r)
			return
		}
		s.serveWebSocket(w, r)
	})
}

func (s *Server) Start() error {
	addr := fmt.Sprintf("127.0.0.1:%d", s.cfg.Port)
	listener, err := net.Listen("tcp4", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	slog.Info("daemon listening", "url", "ws://"+addr+"/ws")
	err = s.http.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.cancel()
	s.registry.ShutdownAll()
	return s.http.Shutdown(ctx)
}

func (s *Server) forgetSeen(threadID string, ids ...string) {
	s.seenMu.Lock()
	defer s.seenMu.Unlock()
	set := s.seen[threadID]
	for _, id := range ids {
		delete(set, id)
	}
}

func (s *Server) noteEvent(threadID string, evt events.Event) {
	s.seenMu.Lock()
	defer s.seenMu.Unlock()
	set := s.seen[threadID]
	if set == nil {
		set = make(map[string]struct{})
		s.seen[threadID] = set
	}
	for _, id := range evt.CausedBy {
		if id != "" {
			set[id] = struct{}{}
		}
	}
	switch evt.Type {
	case events.KindRunStarted:
		s.active[threadID] = evt.RunID
	case events.KindRunFinished, events.KindRunError:
		if s.active[threadID] == evt.RunID {
			delete(s.active, threadID)
		}
	case events.KindCustom:
		if evt.Name == "status."+bus.StatusInboxDropped {
			for _, id := range evt.CausedBy {
				delete(set, id)
			}
		}
	}
}

func (s *Server) isActiveRun(threadID, runID string) bool {
	s.seenMu.Lock()
	defer s.seenMu.Unlock()
	return s.active[threadID] == runID
}

func (s *Server) reserveUnseenTail(threadID string, messages []AGUIMessage) ([]AGUIMessage, error) {
	if len(messages) == 0 {
		return nil, errors.New("messages must not be empty")
	}
	s.seenMu.Lock()
	defer s.seenMu.Unlock()
	set := s.seen[threadID]
	if set == nil {
		set = make(map[string]struct{})
		s.seen[threadID] = set
	}

	i := len(messages) - 1
	if messages[i].Role != "user" {
		return nil, errors.New("messages must end with a user message")
	}
	tailIDs := make(map[string]struct{})
	for i >= 0 && messages[i].Role == "user" {
		id := messages[i].ID
		if id == "" {
			return nil, errors.New("user message id is required")
		}
		if _, exists := set[id]; exists {
			break
		}
		if _, duplicate := tailIDs[id]; duplicate {
			return nil, fmt.Errorf("duplicate user message id %q", id)
		}
		tailIDs[id] = struct{}{}
		i--
	}
	tail := append([]AGUIMessage(nil), messages[i+1:]...)
	for _, message := range tail {
		set[message.ID] = struct{}{}
	}
	return tail, nil
}

func validThreadID(id string) bool { return safeThreadID.MatchString(id) }
