package setup

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/agents"
	"github.com/basenana/friday/core/agents/summarize"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/providers/anthropics"
	"github.com/basenana/friday/core/providers/openai"
	coreSession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/sandbox"
	"github.com/basenana/friday/skills"
	"github.com/basenana/friday/workspace"
)

type AgentContext struct {
	Client    providers.Client
	Workspace *workspace.Workspace
	Session   *coreSession.Session
	Agent     agents.Agent
}

type Option func(*options)

type options struct {
	sessionID  string
	isolate    bool
	sessionMgr SessionManager
	extraTools []*tools.Tool
}

type SessionManager interface {
	GetOrCreateByID(sessionID string, opts ...coreSession.Option) (*coreSession.Session, bool, error)
	CreateIsolated(opts ...coreSession.Option) (*coreSession.Session, string, error)
	GetOrCreateCurrent(opts ...coreSession.Option) (*coreSession.Session, string, bool, error)
}

func WithSessionID(id string) Option {
	return func(o *options) {
		o.sessionID = id
	}
}

func WithIsolate(v bool) Option {
	return func(o *options) {
		o.isolate = v
	}
}

func WithSessionManager(mgr SessionManager) Option {
	return func(o *options) {
		o.sessionMgr = mgr
	}
}

func WithExtraTools(t []*tools.Tool) Option {
	return func(o *options) {
		o.extraTools = t
	}
}

func NewAgent(ctx context.Context, cfg *config.Config, opts ...Option) (*AgentContext, error) {
	options := &options{}
	for _, opt := range opts {
		opt(options)
	}

	client, err := createProviderClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("create provider client: %w", err)
	}

	ws := workspace.NewWorkspace(cfg.WorkspacePath(), cfg.MemoryPath())
	if err = ws.EnsureDir(""); err != nil {
		return nil, fmt.Errorf("create workspace: %w", err)
	}

	var sess *coreSession.Session

	if options.sessionMgr != nil {
		sessionOpts := []coreSession.Option{coreSession.WithWorkdirFS(ws)}

		if options.sessionID != "" {
			sess, _, err = options.sessionMgr.GetOrCreateByID(options.sessionID, sessionOpts...)
		} else if options.isolate {
			sess, _, err = options.sessionMgr.CreateIsolated(sessionOpts...)
		} else {
			sess, _, _, err = options.sessionMgr.GetOrCreateCurrent(sessionOpts...)
		}
		if err != nil {
			return nil, fmt.Errorf("get/create session: %w", err)
		}

		compactThreshold := int64(float64(cfg.Model.MaxTokens) * 0.85)
		compactHook := summarize.NewCompactHook(client, compactThreshold)
		sess.RegisterHook(compactHook)

		skillLoader := skills.NewLoader(ws.SkillsPath())
		if err := skillLoader.Load(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to load skills: %v\n", err)
		}
		skillRegistry := skills.NewRegistry(skillLoader)
		skillHook := skills.NewHook(skillRegistry)
		sess.RegisterHook(skillHook)
	}

	wsContent, err := ws.Load()
	if err != nil {
		return nil, fmt.Errorf("load workspace: %w", err)
	}

	if sess != nil && wsContent != nil && len(wsContent.MemoryHistory) > 0 && len(sess.History) == 0 {
		sess.History = append(wsContent.MemoryHistory, sess.History...)
	}

	var allTools []*tools.Tool
	sandboxCfg := cfg.Sandbox
	if sandboxCfg == nil {
		sandboxCfg = sandbox.DefaultConfig()
	}
	sandboxExec := sandbox.NewExecutor(sandboxCfg)
	bashTool := sandbox.NewBashTool(sandboxExec)
	allTools = append(ws.FsTools(), bashTool)

	if len(options.extraTools) > 0 {
		allTools = append(allTools, options.extraTools...)
	}

	agent := agents.New(client, agents.Option{
		SystemPrompt: workspace.ComposeSystemPrompt(wsContent), // always use this prompts
		Tools:        allTools,
	})

	return &AgentContext{
		Client:    client,
		Workspace: ws,
		Session:   sess,
		Agent:     agent,
	}, nil
}

func createProviderClient(cfg *config.Config) (providers.Client, error) {
	provider := strings.ToLower(cfg.Model.Provider)

	switch provider {
	case "anthropic":
		host := cfg.Model.BaseURL
		if host == "" {
			host = "https://api.anthropic.com"
		}
		temp := cfg.Model.Temperature
		maxTokens := int64(cfg.Model.MaxTokens)
		return anthropics.New(host, cfg.Model.Key, anthropics.Model{
			Name:        cfg.Model.Model,
			Temperature: &temp,
			MaxTokens:   &maxTokens,
			QPM:         cfg.Model.QPM,
			Proxy:       cfg.Model.Proxy,
		}), nil
	case "openai", "":
		host := cfg.Model.BaseURL
		if host == "" {
			host = "https://api.openai.com/v1"
		}
		temp := cfg.Model.Temperature
		return openai.New(host, cfg.Model.Key, openai.Model{
			Name:        cfg.Model.Model,
			Temperature: &temp,
			QPM:         cfg.Model.QPM,
		}), nil
	default:
		return nil, fmt.Errorf("unknown provider: %s", provider)
	}
}
