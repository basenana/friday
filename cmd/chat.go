package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/agents"
	"github.com/basenana/friday/core/agents/summarize"
	"github.com/basenana/friday/core/api"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/providers/anthropics"
	"github.com/basenana/friday/core/providers/openai"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/fs"
	"github.com/basenana/friday/memory"
	"github.com/basenana/friday/session/file"
)

func runChat(cfg *config.Config, args []string) {
	// Parse chat-specific flags using flag.FlagSet
	chatFlags := flag.NewFlagSet("chat", flag.ContinueOnError)
	sessionFlag := chatFlags.String("session", "", "session ID to use")
	if err := chatFlags.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "failed to parse flags: %v\n", err)
		os.Exit(1)
	}

	ctx := context.Background()

	// Create client based on Provider
	var client providers.Client
	provider := strings.ToLower(cfg.Model.Provider)

	switch provider {
	case "anthropic":
		host := cfg.Model.BaseURL
		if host == "" {
			host = "https://api.anthropic.com"
		}
		temp := cfg.Model.Temperature
		maxTokens := int64(cfg.Model.MaxTokens)
		client = anthropics.New(host, cfg.Model.Key, anthropics.Model{
			Name:        cfg.Model.Model,
			Temperature: &temp,
			MaxTokens:   &maxTokens,
			QPM:         cfg.Model.QPM,
			Proxy:       cfg.Model.Proxy,
		})
		fmt.Printf("Using Anthropic client: %s (model: %s)\n", host, cfg.Model.Model)
	case "openai", "":
		host := cfg.Model.BaseURL
		if host == "" {
			host = "https://api.openai.com/v1"
		}
		temp := cfg.Model.Temperature
		client = openai.New(host, cfg.Model.Key, openai.Model{
			Name:        cfg.Model.Model,
			Temperature: &temp,
			QPM:         cfg.Model.QPM,
		})
		fmt.Printf("Using OpenAI client: %s (model: %s)\n", host, cfg.Model.Model)
	default:
		fmt.Printf("Unknown provider: %s, defaulting to OpenAI\n", provider)
		host := cfg.Model.BaseURL
		if host == "" {
			host = "https://api.openai.com/v1"
		}
		temp := cfg.Model.Temperature
		client = openai.New(host, cfg.Model.Key, openai.Model{
			Name:        cfg.Model.Model,
			Temperature: &temp,
			QPM:         cfg.Model.QPM,
		})
	}

	sessionStore := file.NewFileSessionStore(cfg.SessionsPath())
	if err := sessionStore.EnsureDir(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create session dir: %v\n", err)
		os.Exit(1)
	}

	// Get or create session
	var sess *session.Session
	var sessionID string

	workdir := fs.NewFileSystem(cfg.WorkspacePath())
	if err := workdir.EnsureDir(""); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create workspace: %v\n", err)
		os.Exit(1)
	}

	sessionOpts := []session.Option{session.WithWorkdirFS(workdir)}

	// First check --session flag
	if *sessionFlag != "" {
		// Try to load existing session
		loadedSess, err := sessionStore.Load(*sessionFlag, sessionOpts...)
		if err != nil {
			// Session doesn't exist, create new one
			sessionID = *sessionFlag
			alias := time.Now().Format("2006-01-02")
			sess, err = sessionStore.Create(sessionID, sessionOpts...)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to create session: %v\n", err)
				os.Exit(1)
			}
			sessionStore.UpdateAlias(sessionID, alias)
			SetCurrentSession(cfg, sessionID)
			fmt.Printf("Created new session: %s (alias: %s)\n", sessionID[:8], alias)
		} else {
			sess = loadedSess
			sessionID = *sessionFlag
			fmt.Printf("Using specified session: %s (loaded %d messages)\n", sessionID[:8], len(sess.History))
		}
	} else {
		// Fall back to current session
		currentID, err := GetCurrentSession(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to get current session: %v\n", err)
			os.Exit(1)
		}

		// Try to load current session
		if currentID != "" {
			loadedSess, err := sessionStore.Load(currentID, sessionOpts...)
			if err == nil {
				sess = loadedSess
				sessionID = currentID
				fmt.Printf("Using current session: %s (loaded %d messages)\n", sessionID[:8], len(sess.History))
			}
		}

		// If no session, create new one
		if sess == nil {
			sessionID = types.NewID()
			alias := time.Now().Format("2006-01-02")
			sess, err = sessionStore.Create(sessionID, sessionOpts...)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to create session: %v\n", err)
				os.Exit(1)
			}
			sessionStore.UpdateAlias(sessionID, alias)
			SetCurrentSession(cfg, sessionID)
			fmt.Printf("Created new session: %s (alias: %s)\n", sessionID[:8], alias)
		}
	}

	// Register compact hook (threshold = 85% of MaxTokens)
	compactThreshold := int64(float64(cfg.Model.MaxTokens) * 0.85)
	compactHook := summarize.NewCompactHook(client, compactThreshold)
	sess.RegisterHook(compactHook)
	fmt.Printf("Compact hook registered (threshold: %d tokens)\n", compactThreshold)

	agent := agents.New(client, agents.Option{})

	memSys := memory.NewMemorySystem(cfg.MemoryPath(), cfg.Memory.Days)
	if cfg.Memory.Enabled {
		if err := memSys.EnsureTodayLog(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to ensure memory log: %v\n", err)
		}
	}

	reader := bufio.NewReader(os.Stdin)
	fmt.Println("Friday CLI - Type 'exit' to quit")
	fmt.Println("")

	for {
		fmt.Print("> ")
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}

		line = strings.TrimSpace(line)
		if line == "" || line == "exit" {
			continue
		}

		req := &api.Request{
			Session:     sess,
			UserMessage: line,
		}

		resp := agent.Chat(ctx, req)

		for delta := range resp.Deltas() {
			fmt.Print(delta.Content)
		}
		fmt.Println("")
	}
}
