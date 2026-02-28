package main

import (
	"fmt"
	"os"
	"time"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/session/file"
)

func runSession(cfg *config.Config, args []string) {
	if len(args) == 0 {
		printSessionUsage()
		os.Exit(1)
	}

	store := file.NewFileSessionStore(cfg.SessionsPath())
	if err := store.EnsureDir(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create session dir: %v\n", err)
		os.Exit(1)
	}

	switch args[0] {
	case "list":
		listSessions(store)
	case "new":
		newSession(store)
	case "show":
		if len(args) < 2 {
			fmt.Println("session show requires <id>")
			os.Exit(1)
		}
		showSession(store, args[1])
	default:
		fmt.Printf("unknown session command: %s\n", args[0])
		printSessionUsage()
		os.Exit(1)
	}
}

func listSessions(store *file.FileSessionStore) {
	metas, err := store.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to list sessions: %v\n", err)
		os.Exit(1)
	}

	if len(metas) == 0 {
		fmt.Println("No sessions found")
		return
	}

	fmt.Println("Sessions:")
	for _, meta := range metas {
		fmt.Printf("  %s  (created: %s, messages: %d)\n",
			meta.ID, meta.CreatedAt.Format("2006-01-02 15:04"), meta.MessageCount)
	}
}

func newSession(store *file.FileSessionStore) {
	sessionID := types.NewID()
	_, err := store.Create(sessionID, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create session: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Created session: %s\n", sessionID)
}

func showSession(store *file.FileSessionStore, sessionID string) {
	messages, err := store.LoadMessages(sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load session: %v\n", err)
		os.Exit(1)
	}

	metaPath := store.MetaPath(sessionID)
	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to read meta: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Session: %s\n", sessionID)
	fmt.Printf("Created: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Printf("Messages: %d\n", len(messages))
	fmt.Println("")
	fmt.Println(string(metaData))
	fmt.Println("")

	for i, msg := range messages {
		fmt.Printf("[%d] %s\n", i+1, formatMessage(msg))
	}
}

func formatMessage(msg types.Message) string {
	if msg.UserMessage != "" {
		return fmt.Sprintf("user: %s", msg.UserMessage)
	}
	if msg.AgentMessage != "" {
		return fmt.Sprintf("assistant: %s", msg.AgentMessage)
	}
	if msg.ToolName != "" {
		return fmt.Sprintf("tool(%s): %s", msg.ToolName, msg.ToolContent)
	}
	return "unknown"
}

func printSessionUsage() {
	fmt.Println("Usage: friday session <command>")
	fmt.Println("")
	fmt.Println("Commands:")
	fmt.Println("  list           list all sessions")
	fmt.Println("  new           create new session")
	fmt.Println("  show <id>     show session details")
}
