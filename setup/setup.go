package setup

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/agents"
	"github.com/basenana/friday/core/agents/summarize"
	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/planning"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/providers/anthropics"
	"github.com/basenana/friday/core/providers/openai"
	coreSession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/tools"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/memory"
	"github.com/basenana/friday/sandbox"
	"github.com/basenana/friday/skills"
	"github.com/basenana/friday/workspace"
)

type AgentContext struct {
	Client    providers.Client
	Workspace *workspace.Workspace
	Session   *coreSession.Session
	Agent     agents.Agent
	Memory    *memory.MemorySystem
}

type Option func(*options)

type options struct {
	sessionID  string
	isolate    bool
	temporary  bool
	verbose    bool
	extraTools []*tools.Tool
}

type SessionManager interface {
	SetLLM(llm providers.Client)
	GetOrCreateByID(sessionID string, opts ...coreSession.Option) (*coreSession.Session, bool, error)
	CreateIsolated(opts ...coreSession.Option) (*coreSession.Session, string, error)
	CreateTemporary(opts ...coreSession.Option) (*coreSession.Session, string, error)
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

func WithTemporary(v bool) Option {
	return func(o *options) {
		o.temporary = v
	}
}

func WithVerbose(v bool) Option {
	return func(o *options) {
		o.verbose = v
	}
}

func WithExtraTools(t []*tools.Tool) Option {
	return func(o *options) {
		o.extraTools = t
	}
}

func NewAgent(sessionMgr SessionManager, cfg *config.Config, opts ...Option) (*AgentContext, error) {
	options := &options{}
	for _, opt := range opts {
		opt(options)
	}

	client, err := CreateProviderClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("create provider client: %w", err)
	}

	sessionMgr.SetLLM(client)

	ws := workspace.NewWorkspace(cfg.WorkspacePath(), cfg.MemoryPath())
	if err = ws.EnsureDir(""); err != nil {
		return nil, fmt.Errorf("create workspace: %w", err)
	}

	fileState := workspace.NewFileState(cfg.StatePath())
	sessionOpts := []coreSession.Option{coreSession.WithState(fileState)}

	var sess *coreSession.Session
	switch {
	case options.sessionID != "":
		sess, _, err = sessionMgr.GetOrCreateByID(options.sessionID, sessionOpts...)

	case options.isolate:
		sess, _, err = sessionMgr.CreateIsolated(sessionOpts...)

	case options.temporary:
		sess, _, err = sessionMgr.CreateTemporary(sessionOpts...)

	default:
		sess, _, _, err = sessionMgr.GetOrCreateCurrent(sessionOpts...)
	}
	if err != nil {
		return nil, fmt.Errorf("get/create session: %w", err)
	}

	if options.verbose {
		sessionType := ""
		if options.isolate {
			sessionType = " (isolated)"
		}
		if options.temporary {
			sessionType = " (temporary)"
		}
		if len(sess.History) == 0 {
			fmt.Printf("Created new session%s\n", sessionType)
		} else {
			fmt.Printf("Using session%s (loaded %d messages)\n", sessionType, len(sess.History))
		}
	}

	compactThreshold := int64(float64(cfg.Model.MaxTokens) * 0.85)
	compactHook := summarize.NewCompactHook(client, compactThreshold)
	sess.RegisterHook(compactHook)

	sess.RegisterHook(planning.New(client, planning.Option{}))

	skillLoader := skills.NewLoader(ws.SkillsPath())
	if err := skillLoader.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to load skills: %v\n", err)
	}
	skillRegistry := skills.NewRegistry(skillLoader)
	skillHook := skills.NewHook(skillRegistry)
	sess.RegisterHook(skillHook)

	wsContent, err := ws.Load()
	if err != nil {
		return nil, fmt.Errorf("load workspace: %w", err)
	}

	if wsContent != nil && len(wsContent.MemoryHistory) > 0 && len(sess.History) == 0 {
		sess.History = append(wsContent.MemoryHistory, sess.History...)
	}

	workdir, _ := os.Getwd()

	var allTools []*tools.Tool
	sandboxCfg := cfg.Sandbox
	if sandboxCfg == nil {
		sandboxCfg = sandbox.DefaultConfig()
	}
	sandboxExec := sandbox.NewExecutor(sandboxCfg)
	fsTools := sandbox.NewFsTools(sandboxExec, workdir)
	bashTool := sandbox.NewBashTool(sandboxExec, workdir)
	allTools = append(fsTools, bashTool)

	if len(options.extraTools) > 0 {
		allTools = append(allTools, options.extraTools...)
	}

	agent := agents.New(client, agents.Option{
		SystemPrompt: workspace.ComposeSystemPrompt(wsContent),
		Tools:        allTools,
	})

	memSys := memory.NewMemorySystem(cfg.MemoryPath(), cfg.Memory.Days)
	if err = memSys.EnsureTodayMemory(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to ensure memory log: %v\n", err)
	}

	return &AgentContext{
		Client:    client,
		Workspace: ws,
		Session:   sess,
		Agent:     agent,
		Memory:    memSys,
	}, nil
}

func (ac *AgentContext) Chat(ctx context.Context, message string, image *types.ImageContent) *api.Response {
	req := &api.Request{
		Session:     ac.Session,
		UserMessage: message,
		Image:       image,
	}
	return ac.Agent.Chat(ctx, req)
}

func PrintResponse(resp *api.Response) {
	hasOutput := false
Waiting:
	for {
		select {
		case err := <-resp.Error():
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				return
			}
		case delta, ok := <-resp.Deltas():
			if !ok {
				break Waiting
			}
			if !hasOutput && strings.TrimSpace(delta.Content) == "" {
				continue
			}
			hasOutput = true
			fmt.Print(delta.Content)
		}
	}
	fmt.Println()
}

func CreateProviderClient(cfg *config.Config) (providers.Client, error) {
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
