package main

import (
	"context"
	"fmt"
	"os"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/agents"
	"github.com/basenana/friday/core/agents/summarize"
	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/providers"
	coreSession "github.com/basenana/friday/core/session"
	"github.com/basenana/friday/memory"
	"github.com/basenana/friday/sandbox"
	"github.com/basenana/friday/sessions"
	"github.com/basenana/friday/skills"
	"github.com/basenana/friday/workspace"
)

// AgentContext holds all components needed to interact with an agent
type AgentContext struct {
	Client    providers.Client
	Workspace *workspace.Workspace
	Session   *coreSession.Session
	Agent     agents.Agent
	Memory    *memory.MemorySystem
}

// AgentOption configures how the agent context is set up
type AgentOption func(*agentOptions)

type agentOptions struct {
	sessionID string
	verbose   bool
	isolate   bool
}

// WithSessionID specifies a specific session to use
func WithSessionID(id string) AgentOption {
	return func(o *agentOptions) {
		o.sessionID = id
	}
}

// WithVerbose enables verbose output
func WithVerbose(v bool) AgentOption {
	return func(o *agentOptions) {
		o.verbose = v
	}
}

// WithIsolate creates an isolated session that won't become the current session
func WithIsolate(v bool) AgentOption {
	return func(o *agentOptions) {
		o.isolate = v
	}
}

// SetupAgent initializes all components needed to run an agent
func SetupAgent(ctx context.Context, cfg *config.Config, sessMgr *sessions.Manager, opts ...AgentOption) (*AgentContext, error) {
	options := &agentOptions{}
	for _, opt := range opts {
		opt(options)
	}

	// Create provider client
	client, err := createClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("create provider client: %w", err)
	}

	// Initialize workspace
	ws := workspace.NewWorkspace(cfg.WorkspacePath(), cfg.MemoryPath())
	if err = ws.EnsureDir(""); err != nil {
		return nil, fmt.Errorf("create workspace: %w", err)
	}

	// Get or create session
	sessionOpts := []coreSession.Option{coreSession.WithWorkdirFS(ws)}
	var sess *coreSession.Session
	var sessionID string
	var created bool

	if options.sessionID != "" {
		sess, created, err = sessMgr.GetOrCreateByID(options.sessionID, sessionOpts...)
		sessionID = options.sessionID
	} else if options.isolate {
		sess, sessionID, err = sessMgr.CreateIsolated(sessionOpts...)
		created = true
	} else {
		sess, sessionID, created, err = sessMgr.GetOrCreateCurrent(sessionOpts...)
	}
	if err != nil {
		return nil, fmt.Errorf("get/create session: %w", err)
	}

	if options.verbose {
		sessionType := ""
		if options.isolate {
			sessionType = " (isolated)"
		}
		if created {
			fmt.Printf("Created new session%s: %s\n", sessionType, sessionID[:8])
		} else {
			fmt.Printf("Using session%s: %s (loaded %d messages)\n", sessionType, sessionID[:8], len(sess.History))
		}
	}

	// Register compact hook
	compactThreshold := int64(float64(cfg.Model.MaxTokens) * 0.85)
	compactHook := summarize.NewCompactHook(client, compactThreshold)
	sess.RegisterHook(compactHook)

	// Initialize skills
	skillLoader := skills.NewLoader(ws.SkillsPath())
	if err := skillLoader.Load(); err != nil {
		// Non-fatal, just log
		fmt.Fprintf(os.Stderr, "Warning: failed to load skills: %v\n", err)
	} else if options.verbose {
		skillCount := len(skillLoader.List())
		if skillCount > 0 {
			fmt.Printf("Loaded %d skills\n", skillCount)
		}
	}

	// Register skill hook
	skillRegistry := skills.NewRegistry(skillLoader)
	skillHook := skills.NewHook(skillRegistry)
	sess.RegisterHook(skillHook)

	// Load workspace content
	wsContent, err := ws.Load()
	if err != nil {
		return nil, fmt.Errorf("load workspace: %w", err)
	}

	// Prepend memory history to session if this is a new session
	if wsContent != nil && len(wsContent.MemoryHistory) > 0 && len(sess.History) == 0 {
		sess.History = append(wsContent.MemoryHistory, sess.History...)
		if options.verbose {
			fmt.Printf("Loaded %d memory messages\n", len(wsContent.MemoryHistory))
		}
	}

	// Initialize sandbox and bash tool
	sandboxCfg := cfg.Sandbox
	if sandboxCfg == nil {
		sandboxCfg = sandbox.DefaultConfig()
	}
	sandboxExec := sandbox.NewExecutor(sandboxCfg)
	bashTool := sandbox.NewBashTool(sandboxExec)

	// Collect all tools
	allTools := append(ws.FsTools(), bashTool)

	// Create agent
	systemPrompt := workspace.ComposeSystemPrompt(wsContent)
	agent := agents.New(client, agents.Option{
		SystemPrompt: systemPrompt,
		Tools:        allTools,
	})

	// Ensure memory log exists
	memSys := memory.NewMemorySystem(cfg.MemoryPath(), cfg.Memory.Days)
	if err = memSys.EnsureTodayMemory(); err != nil {
		// Non-fatal, just log
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

// Chat sends a message to the agent and returns the response
func (ac *AgentContext) Chat(ctx context.Context, message string) *api.Response {
	req := &api.Request{
		Session:     ac.Session,
		UserMessage: message,
	}
	return ac.Agent.Chat(ctx, req)
}

// PrintResponse streams the response to stdout
func PrintResponse(resp *api.Response) {
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
			fmt.Print(delta.Content)
		}
	}
	fmt.Println()
}
