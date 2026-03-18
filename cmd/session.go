package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basenana/friday/core/types"
	"github.com/basenana/friday/sessions"
	"github.com/basenana/friday/sessions/file"
	"github.com/basenana/friday/setup"
)

// sessionCmd represents the session command
var sessionCmd = &cobra.Command{
	Use:   "sessions",
	Short: "Manage chat sessions",
	Long:  `Manage chat sessions for the Friday AI assistant.`,
}

// sessionListCmd represents the session list command
var sessionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List active sessions",
	Long:  `List all active chat sessions. The current session is marked with '*'.`,
	Run: func(cmd *cobra.Command, args []string) {
		store := sessMgr.GetStore()
		metas, err := store.ListActive()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to list sessions: %v\n", err)
			os.Exit(1)
		}

		if len(metas) == 0 {
			fmt.Println("No sessions found")
			return
		}

		currentID, _ := sessMgr.GetCurrentID()

		fmt.Println("Sessions:")
		for _, meta := range metas {
			marker := " "
			if meta.ID == currentID {
				marker = "*"
			}
			alias := meta.Alias
			if alias == "" {
				alias = "-"
			}
			fmt.Printf("  %s  %s %s %s (messages: %d)\n",
				marker, meta.ID, meta.CreatedAt.Format("2006-01-02 15:04"), alias, meta.MessageCount)
		}
	},
}

// sessionNewCmd represents the session new command
var sessionNewCmd = &cobra.Command{
	Use:   "new",
	Short: "Create a new session",
	Long:  `Create a new chat session.`,
	Run: func(cmd *cobra.Command, args []string) {
		store := sessMgr.GetStore()
		sessionID := types.NewID()

		_, err := store.Create(sessionID, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to create session: %v\n", err)
			os.Exit(1)
		}

		// Set alias based on tty
		alias := sessMgr.Alias()
		if alias != "" {
			if err := store.UpdateAlias(sessionID, alias); err != nil {
				fmt.Fprintf(os.Stderr, "failed to set alias: %v\n", err)
			}
		}

		if err := sessMgr.SetCurrentID(sessionID); err != nil {
			fmt.Fprintf(os.Stderr, "failed to set current session: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Created session: %s\n", sessionID)
	},
}

// sessionCurrentCmd represents the session current command
var sessionCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Show current session",
	Long:  `Display the ID of the current session.`,
	Run: func(cmd *cobra.Command, args []string) {
		id, err := sessMgr.GetCurrentID()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to get current session: %v\n", err)
			os.Exit(1)
		}
		if id == "" {
			fmt.Println("No current session")
			return
		}
		fmt.Printf("Current session: %s\n", id)
	},
}

// sessionUseCmd represents the session use command
var sessionUseCmd = &cobra.Command{
	Use:   "use <id>",
	Short: "Switch to a session",
	Long:  `Switch to the specified session by ID.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		store := sessMgr.GetStore()
		prefix := args[0]

		metas, err := store.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to list sessions: %v\n", err)
			os.Exit(1)
		}

		sessionID, found := findSessionByPrefix(metas, prefix)
		if !found {
			fmt.Printf("Session not found: %s\n", prefix)
			os.Exit(1)
		}

		if err := sessMgr.SetCurrentID(sessionID); err != nil {
			fmt.Fprintf(os.Stderr, "failed to set current session: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Switched to session: %s\n", sessionID)
	},
}

// sessionShowCmd represents the session show command
var sessionShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show session details",
	Long:  `Display details and message history of a session.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		store := sessMgr.GetStore()
		prefix := args[0]

		metas, err := store.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to list sessions: %v\n", err)
			os.Exit(1)
		}

		sessionID, found := findSessionByPrefix(metas, prefix)
		if !found {
			fmt.Printf("Session not found: %s\n", prefix)
			os.Exit(1)
		}

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
			timeStr := ""
			if !msg.Time.IsZero() {
				timeStr = msg.Time.Format("15:04:05")
			}
			fmt.Printf("[%d] %s %s\n", idx, timeStr, formatMessage(msg))
		}
	},
}

// sessionAliasCmd represents the session alias command
var sessionAliasCmd = &cobra.Command{
	Use:   "alias <id> <name>",
	Short: "Set session alias",
	Long:  `Set an alias name for a session.`,
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		store := sessMgr.GetStore()
		prefix := args[0]
		alias := args[1]

		metas, err := store.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to list sessions: %v\n", err)
			os.Exit(1)
		}

		sessionID, found := findSessionByPrefix(metas, prefix)
		if !found {
			fmt.Printf("Session not found: %s\n", prefix)
			os.Exit(1)
		}

		if err := store.UpdateAlias(sessionID, alias); err != nil {
			fmt.Fprintf(os.Stderr, "failed to set alias: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Set alias '%s' for session: %s\n", alias, sessionID)
	},
}

// sessionArchiveCmd represents the session archive command
var sessionArchiveCmd = &cobra.Command{
	Use:   "archive <id>",
	Short: "Archive a session",
	Long:  `Archive a session to hide it from the active list.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		store := sessMgr.GetStore()
		prefix := args[0]

		metas, err := store.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to list sessions: %v\n", err)
			os.Exit(1)
		}

		sessionID, found := findSessionByPrefix(metas, prefix)
		if !found {
			fmt.Printf("Session not found: %s\n", prefix)
			os.Exit(1)
		}

		if err := store.Archive(sessionID); err != nil {
			fmt.Fprintf(os.Stderr, "failed to archive: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Archived session: %s\n", sessionID)

		// If current session, clear it
		currentID, _ := sessMgr.GetCurrentID()
		if currentID == sessionID {
			os.Remove(cfg.DataDirPath() + "/current")
		}
	},
}

// sessionUnarchiveCmd represents the session unarchive command
var sessionUnarchiveCmd = &cobra.Command{
	Use:   "unarchive <id>",
	Short: "Unarchive a session",
	Long:  `Unarchive a session to restore it to the active list.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		store := sessMgr.GetStore()
		prefix := args[0]

		metas, err := store.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to list sessions: %v\n", err)
			os.Exit(1)
		}

		sessionID, found := findSessionByPrefix(metas, prefix)
		if !found {
			fmt.Printf("Session not found: %s\n", prefix)
			os.Exit(1)
		}

		if err := store.Unarchive(sessionID); err != nil {
			fmt.Fprintf(os.Stderr, "failed to unarchive: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Unarchived session: %s\n", sessionID)
	},
}

// sessionArchivedCmd represents the session archived command
var sessionArchivedCmd = &cobra.Command{
	Use:   "archived",
	Short: "List archived sessions",
	Long:  `List all archived sessions.`,
	Run: func(cmd *cobra.Command, args []string) {
		store := sessMgr.GetStore()
		metas, err := store.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to list sessions: %v\n", err)
			os.Exit(1)
		}

		var archived []sessions.SessionMeta
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
	},
}

// sessionDeleteCmd represents the session delete command
var sessionDeleteCmd = &cobra.Command{
	Use:   "delete <id>",
	Short: "Delete a session",
	Long:  `Delete a session permanently.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		store := sessMgr.GetStore()
		prefix := args[0]

		metas, err := store.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to list sessions: %v\n", err)
			os.Exit(1)
		}

		sessionID, found := findSessionByPrefix(metas, prefix)
		if !found {
			fmt.Printf("Session not found: %s\n", prefix)
			os.Exit(1)
		}

		if err := store.Delete(sessionID); err != nil {
			fmt.Fprintf(os.Stderr, "failed to delete session: %v\n", err)
			os.Exit(1)
		}

		currentID, _ := sessMgr.GetCurrentID()
		if currentID == sessionID {
			os.Remove(cfg.DataDirPath() + "/current")
		}

		fmt.Printf("Deleted session: %s\n", sessionID)
	},
}

// sessionCompactCmd represents the session compact command
var sessionCompactCmd = &cobra.Command{
	Use:   "compact <id>",
	Short: "Compact a session",
	Long:  `Compact a session by summarizing its history to reduce token count.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		store := sessMgr.GetStore()
		prefix := args[0]

		metas, err := store.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to list sessions: %v\n", err)
			os.Exit(1)
		}

		sessionID, found := findSessionByPrefix(metas, prefix)
		if !found {
			fmt.Printf("Session not found: %s\n", prefix)
			os.Exit(1)
		}

		client, err := setup.CreateProviderClient(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to create provider: %v\n", err)
			os.Exit(1)
		}

		fileStore := file.NewFileSessionStore(cfg.SessionsPath())
		sess, err := fileStore.Load(sessionID, client)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to load session: %v\n", err)
			os.Exit(1)
		}

		beforeTokens := sess.Tokens()
		beforeCount := len(sess.History)
		fmt.Printf("Compacting session: %s\n", sessionID)
		fmt.Printf("  Before: %d messages, %d tokens\n", beforeCount, beforeTokens)

		if err := sess.CompactHistory(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "failed to compact: %v\n", err)
			os.Exit(1)
		}

		if err := sess.ReplaceHistory(sess.History...); err != nil {
			fmt.Fprintf(os.Stderr, "failed to persist: %v\n", err)
			os.Exit(1)
		}

		afterTokens := sess.Tokens()
		afterCount := len(sess.History)
		fmt.Printf("  After: %d messages, %d tokens\n", afterCount, afterTokens)
		if beforeTokens > 0 {
			fmt.Printf("  Reduced: %d%% (%d tokens)\n",
				(beforeTokens-afterTokens)*100/beforeTokens,
				beforeTokens-afterTokens)
		}
	},
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

func findSessionByPrefix(metas []sessions.SessionMeta, prefix string) (string, bool) {
	var matches []sessions.SessionMeta
	for _, meta := range metas {
		if strings.HasPrefix(meta.ID, prefix) {
			matches = append(matches, meta)
		}
	}
	if len(matches) == 1 {
		return matches[0].ID, true
	}
	if len(matches) > 1 {
		fmt.Fprintf(os.Stderr, "Ambiguous prefix '%s', matches %d sessions:\n", prefix, len(matches))
		for _, m := range matches {
			alias := m.Alias
			if alias == "" {
				alias = "-"
			}
			fmt.Fprintf(os.Stderr, "  %s  %s\n", m.ID, alias)
		}
		fmt.Fprintf(os.Stderr, "Please use a longer prefix.\n")
	}
	return "", false
}

func init() {
	rootCmd.AddCommand(sessionCmd)
	sessionCmd.AddCommand(sessionListCmd)
	sessionCmd.AddCommand(sessionNewCmd)
	sessionCmd.AddCommand(sessionCurrentCmd)
	sessionCmd.AddCommand(sessionUseCmd)
	sessionCmd.AddCommand(sessionShowCmd)
	sessionCmd.AddCommand(sessionAliasCmd)
	sessionCmd.AddCommand(sessionArchiveCmd)
	sessionCmd.AddCommand(sessionUnarchiveCmd)
	sessionCmd.AddCommand(sessionArchivedCmd)
	sessionCmd.AddCommand(sessionDeleteCmd)
	sessionCmd.AddCommand(sessionCompactCmd)
}
