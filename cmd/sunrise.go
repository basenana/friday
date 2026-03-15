package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/memory"
	"github.com/basenana/friday/sessions"
	"github.com/basenana/friday/setup"
)

var sunriseCmd = &cobra.Command{
	Use:   "sunrise",
	Short: "Daily startup: process past sessions and setup new session",
	Long: `Sunrise initializes a new day by preserving valuable context from past conversations.

This command extracts memories from old sessions and writes them to long-term storage,
ensuring important context persists across sessions. It then creates a fresh session
for the new day, ready for new conversations.`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		err := runSunrise(ctx, cfg, sessMgr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Sunrise failed: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(sunriseCmd)
}

func runSunrise(ctx context.Context, cfg *config.Config, mgr *sessions.Manager) error {
	today := time.Now().Truncate(24 * time.Hour)

	store := mgr.GetStore()
	agentCtx, err := setup.NewAgent(mgr, cfg, setup.WithTemporary(true))
	if err != nil {
		return fmt.Errorf("failed to create agent: %w", err)
	}

	processor := memory.NewProcessor(agentCtx, memory.ProcessorConfig{
		MemoryPath:    cfg.MemoryPath(),
		WorkspacePath: cfg.WorkspacePath(),
		RecentDays:    5,
	})

	allSessions, err := store.ListActive()
	if err != nil {
		return fmt.Errorf("failed to list sessions: %w", err)
	}

	oldSessions := filterOldSessions(allSessions, today)

	if len(oldSessions) == 0 {
		_, newID, err := mgr.CreateIsolated()
		if err != nil {
			return fmt.Errorf("failed to create new session: %w", err)
		}
		if err := mgr.SetCurrentID(newID); err != nil {
			return fmt.Errorf("failed to set current session: %w", err)
		}
		return nil
	}

	for _, meta := range oldSessions {
		messages, err := store.LoadMessages(meta.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to load messages for old session %s: %v\n", meta.ID, err)
			continue
		}

		if len(messages) == 0 {
			fmt.Fprintf(os.Stderr, "session %s has no messages, skipping\n", meta.ID)
			continue
		}

		history := &memory.SessionHistory{
			ID:           meta.ID,
			CreatedAt:    meta.CreatedAt,
			Messages:     messages,
			MessageCount: len(messages),
		}

		fmt.Printf("Processing session %s\n", meta.ID)
		result, err := processor.ProcessSession(ctx, history)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to process session %s: %v\n", meta.ID, err)
			continue
		}
		fmt.Println(result)
	}

	_, newID, err := mgr.CreateIsolated()
	if err != nil {
		return fmt.Errorf("failed to create new session: %w", err)
	}

	if err := mgr.SetCurrentID(newID); err != nil {
		return fmt.Errorf("failed to set current session: %w", err)
	}

	return nil
}

func filterOldSessions(allSessions []sessions.SessionMeta, today time.Time) []sessions.SessionMeta {
	var old []sessions.SessionMeta
	for _, s := range allSessions {
		if s.CreatedAt.Before(today) {
			old = append(old, s)
		}
	}
	return old
}
