package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/session"
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
		listSessions(store, cfg)
	case "new":
		newSession(store, cfg)
	case "show":
		if len(args) < 2 {
			fmt.Println("session show requires <id>")
			os.Exit(1)
		}
		showSession(store, args[1])
	case "use":
		if len(args) < 2 {
			fmt.Println("session use requires <id>")
			os.Exit(1)
		}
		useSession(store, cfg, args[1])
	case "current":
		showCurrent(cfg)
	case "delete", "rm":
		if len(args) < 2 {
			fmt.Println("session delete requires <id>")
			os.Exit(1)
		}
		deleteSession(store, cfg, args[1])
	case "alias":
		if len(args) < 3 {
			fmt.Println("session alias <id> <name>")
			os.Exit(1)
		}
		setAlias(store, cfg, args[1], args[2])
	case "archive":
		if len(args) < 2 {
			fmt.Println("session archive <id>")
			os.Exit(1)
		}
		archiveSession(store, cfg, args[1])
	case "unarchive":
		if len(args) < 2 {
			fmt.Println("session unarchive <id>")
			os.Exit(1)
		}
		unarchiveSession(store, cfg, args[1])
	case "archived":
		listArchived(store)
	default:
		fmt.Printf("unknown session command: %s\n", args[0])
		printSessionUsage()
		os.Exit(1)
	}
}

// Current session management

func currentSessionFile(cfg *config.Config) string {
	return cfg.DataDirPath() + "/current"
}

func GetCurrentSession(cfg *config.Config) (string, error) {
	path := currentSessionFile(cfg)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	id := strings.TrimSpace(string(data))
	return id, nil
}

func SetCurrentSession(cfg *config.Config, sessionID string) error {
	path := currentSessionFile(cfg)
	return os.WriteFile(path, []byte(sessionID+"\n"), 0644)
}

func showCurrent(cfg *config.Config) {
	id, err := GetCurrentSession(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get current session: %v\n", err)
		os.Exit(1)
	}
	if id == "" {
		fmt.Println("No current session")
		return
	}
	fmt.Printf("Current session: %s\n", id)
}

func useSession(store session.Store, cfg *config.Config, sessionID string) error {
	metas, err := store.List()
	if err != nil {
		return err
	}

	found := false
	for _, meta := range metas {
		if meta.ID == sessionID {
			found = true
			break
		}
	}

	if !found {
		fmt.Printf("Session not found: %s\n", sessionID)
		os.Exit(1)
	}

	if err := SetCurrentSession(cfg, sessionID); err != nil {
		fmt.Fprintf(os.Stderr, "failed to set current session: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Switched to session: %s\n", sessionID)
	return nil
}

func deleteSession(store session.Store, cfg *config.Config, sessionID string) error {
	metas, err := store.List()
	if err != nil {
		return err
	}

	found := false
	for _, meta := range metas {
		if meta.ID == sessionID {
			found = true
			break
		}
	}

	if !found {
		fmt.Printf("Session not found: %s\n", sessionID)
		os.Exit(1)
	}

	if err := store.Delete(sessionID); err != nil {
		fmt.Fprintf(os.Stderr, "failed to delete session: %v\n", err)
		os.Exit(1)
	}

	currentID, _ := GetCurrentSession(cfg)
	if currentID == sessionID {
		os.Remove(currentSessionFile(cfg))
	}

	fmt.Printf("Deleted session: %s\n", sessionID)
	return nil
}

func setAlias(store session.Store, cfg *config.Config, sessionID, alias string) error {
	if err := store.UpdateAlias(sessionID, alias); err != nil {
		fmt.Fprintf(os.Stderr, "failed to set alias: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Set alias '%s' for session: %s\n", alias, sessionID)
	return nil
}

func archiveSession(store session.Store, cfg *config.Config, sessionID string) error {
	if err := store.Archive(sessionID); err != nil {
		fmt.Fprintf(os.Stderr, "failed to archive: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Archived session: %s\n", sessionID)

	// If current session, clear it
	currentID, _ := GetCurrentSession(cfg)
	if currentID == sessionID {
		os.Remove(currentSessionFile(cfg))
	}
	return nil
}

func unarchiveSession(store session.Store, cfg *config.Config, sessionID string) error {
	if err := store.Unarchive(sessionID); err != nil {
		fmt.Fprintf(os.Stderr, "failed to unarchive: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Unarchived session: %s\n", sessionID)
	return nil
}

func listArchived(store session.Store) {
	metas, err := store.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to list sessions: %v\n", err)
		os.Exit(1)
	}

	var archived []session.SessionMeta
	for _, meta := range metas {
		if meta.Archived {
			archived = append(archived, meta)
		}
	}

	if len(archived) == 0 {
		fmt.Println("No archived sessions")
		return
	}

	fmt.Println("Archived sessions:")
	for _, meta := range archived {
		alias := meta.Alias
		if alias == "" {
			alias = meta.CreatedAt.Format("2006-01-02")
		}
		fmt.Printf("  %s  %s (archived, messages: %d)\n",
			meta.ID, alias, meta.MessageCount)
	}
}

func listSessions(store session.Store, cfg *config.Config) {
	metas, err := store.ListActive()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to list sessions: %v\n", err)
		os.Exit(1)
	}

	if len(metas) == 0 {
		fmt.Println("No sessions found")
		return
	}

	currentID, _ := GetCurrentSession(cfg)

	fmt.Println("Sessions:")
	for _, meta := range metas {
		marker := " "
		if meta.ID == currentID {
			marker = "*"
		}
		alias := meta.Alias
		if alias == "" {
			alias = meta.CreatedAt.Format("2006-01-02")
		}
		fmt.Printf("  %s %s  %s (messages: %d)\n",
			marker, meta.ID[:8], alias, meta.MessageCount)
	}
}

func newSession(store session.Store, cfg *config.Config) {
	sessionID := types.NewID()
	now := time.Now()
	alias := now.Format("2006-01-02")

	_, err := store.Create(sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create session: %v\n", err)
		os.Exit(1)
	}

	// Set default alias to today's date
	if err := store.UpdateAlias(sessionID, alias); err != nil {
		fmt.Fprintf(os.Stderr, "failed to set alias: %v\n", err)
		os.Exit(1)
	}

	if err := SetCurrentSession(cfg, sessionID); err != nil {
		fmt.Fprintf(os.Stderr, "failed to set current session: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Created session: %s (alias: %s)\n", sessionID[:8], alias)
}

func showSession(store session.Store, sessionID string) {
	messages, err := store.LoadMessages(sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load session: %v\n", err)
		os.Exit(1)
	}

	meta, err := store.GetMeta(sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get meta: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Session: %s\n", sessionID)
	fmt.Printf("Created: %s\n", meta.CreatedAt.Format("2006-01-02 15:04:05"))

	// Count visible messages (exclude agent internal messages)
	visibleCount := 0
	for _, msg := range messages {
		if msg.Role != types.RoleAgent {
			visibleCount++
		}
	}
	fmt.Printf("Messages: %d\n", visibleCount)
	fmt.Println("")

	idx := 0
	for _, msg := range messages {
		if msg.Role == types.RoleAgent {
			continue // skip agent internal messages
		}
		idx++
		fmt.Printf("[%d] %s\n", idx, formatMessage(msg))
	}
}

func formatMessage(msg types.Message) string {
	switch msg.Role {
	case types.RoleUser:
		return fmt.Sprintf("user: %s", msg.Content)
	case types.RoleAssistant:
		if len(msg.ToolCalls) > 0 {
			var calls []string
			for _, tc := range msg.ToolCalls {
				calls = append(calls, fmt.Sprintf("%s(%s)", tc.Name, tc.Arguments))
			}
			return fmt.Sprintf("assistant: [tool_calls] %s", strings.Join(calls, ", "))
		}
		return fmt.Sprintf("assistant: %s", msg.Content)
	case types.RoleTool:
		if msg.ToolResult != nil {
			return fmt.Sprintf("tool: %s", msg.ToolResult.Content)
		}
		return "tool: (empty)"
	case types.RoleSystem:
		return fmt.Sprintf("system: %s", msg.Content)
	default:
		return fmt.Sprintf("unknown(%s): %s", msg.Role, msg.Content)
	}
}

func printSessionUsage() {
	flag.Usage = func() {}
	fmt.Println("Usage: friday session <command>")
	fmt.Println("")
	fmt.Println("Commands:")
	fmt.Println("  list                list active sessions (* = current)")
	fmt.Println("  new                 create new session (alias = today's date)")
	fmt.Println("  use <id>            switch to session")
	fmt.Println("  current             show current session")
	fmt.Println("  show <id>           show session details")
	fmt.Println("  alias <id> <name>   set session alias")
	fmt.Println("  archive <id>        archive session")
	fmt.Println("  unarchive <id>     unarchive session")
	fmt.Println("  archived            list archived sessions")
	fmt.Println("  delete <id>         delete session")
}
