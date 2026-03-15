package main

import (
	"context"
	"fmt"
	"os"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/agents"
	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/memory"
	"github.com/basenana/friday/sessions"
	"github.com/basenana/friday/setup"
	"github.com/basenana/friday/workspace"
)

// AgentContext holds all components needed to interact with an agent
type AgentContext struct {
	Client    providers.Client
	Workspace *workspace.Workspace
	Session   *session.Session
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

	setupOpts := []setup.Option{
		setup.WithSessionManager(sessMgr),
	}
	if options.sessionID != "" {
		setupOpts = append(setupOpts, setup.WithSessionID(options.sessionID))
	}
	if options.isolate {
		setupOpts = append(setupOpts, setup.WithIsolate(true))
	}

	agentCtx, err := setup.NewAgent(ctx, cfg, setupOpts...)
	if err != nil {
		return nil, err
	}

	if options.verbose {
		sessionType := ""
		if options.isolate {
			sessionType = " (isolated)"
		}
		if agentCtx.Session != nil {
			if len(agentCtx.Session.History) == 0 {
				fmt.Printf("Created new session%s\n", sessionType)
			} else {
				fmt.Printf("Using session%s (loaded %d messages)\n", sessionType, len(agentCtx.Session.History))
			}
		}
	}

	// Ensure memory log exists
	memSys := memory.NewMemorySystem(cfg.MemoryPath(), cfg.Memory.Days)
	if err = memSys.EnsureTodayMemory(); err != nil {
		// Non-fatal, just log
		fmt.Fprintf(os.Stderr, "Warning: failed to ensure memory log: %v\n", err)
	}

	return &AgentContext{
		Client:    agentCtx.Client,
		Workspace: agentCtx.Workspace,
		Session:   agentCtx.Session,
		Agent:     agentCtx.Agent,
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
