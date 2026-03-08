package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/agents"
	"github.com/basenana/friday/core/api"
	_ "github.com/basenana/friday/core/providers/anthropics"
	"github.com/basenana/friday/core/providers/openai"
	"github.com/basenana/friday/core/session"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/fs"
	"github.com/basenana/friday/memory"
	"github.com/basenana/friday/session/file"
)

func runChat(cfg *config.Config) {
	ctx := context.Background()

	host := cfg.API.BaseURL
	if host == "" {
		host = "https://api.openai.com/v1"
	}
	client := openai.New(host, cfg.API.Key, openai.Model{Name: cfg.API.Model})

	sessionStore := file.NewFileSessionStore(cfg.SessionsPath())
	if err := sessionStore.EnsureDir(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create session dir: %v\n", err)
		os.Exit(1)
	}

	metas, err := sessionStore.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to list sessions: %v\n", err)
		os.Exit(1)
	}

	var sessionID string
	if len(metas) == 0 {
		sessionID = types.NewID()
	} else {
		sessionID = metas[0].ID
	}

	workdir := fs.NewFileSystem(cfg.WorkspacePath())
	if err := workdir.EnsureDir(""); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create workspace: %v\n", err)
		os.Exit(1)
	}

	sess := session.New(sessionID, client, session.WithWorkdirFS(workdir))

	loadHistoryHook := file.NewLoadHistoryHook(sessionStore)
	_ = loadHistoryHook

	persistHook := file.NewPersistHook(sessionStore)
	_ = persistHook

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

		var fullMessage string
		for delta := range resp.Deltas() {
			fmt.Print(delta.Content)
			fullMessage += delta.Content
		}
		fmt.Println("")

		sessionStore.AppendMessages(sessionID,
			types.Message{UserMessage: line},
			types.Message{AgentMessage: fullMessage},
		)
	}
}
