package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	projectpkg "github.com/basenana/friday/coder/project"
	"github.com/basenana/friday/config"
	"github.com/basenana/friday/sessions"
	"github.com/basenana/friday/tui"
)

var tuiSessionID string

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Start interactive TUI chat session (like claude code / codex)",
	Long: `Launch a full-screen interactive TUI for multi-turn conversations.

Features:
  - Streaming markdown-rendered responses
  - Reasoning, tool calls, rich cards, and interactive forms
  - Slash command completion and prompt history
  - Enter to steer and Tab to queue while a task runs
  - Adaptive alternate-screen behavior`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get project directory: %w", err)
		}
		return runTUI(cmd.Context(), cwd, tuiSessionID, sessMgr, cfg, tui.RunProject, func(cfg *config.Config, cwd string) error {
			return tui.RunWorktree(sessMgr, cfg, cwd)
		})
	},
}

func runTUI(
	ctx context.Context,
	cwd, sessionID string,
	manager *sessions.Manager,
	cfg *config.Config,
	runProject func(*projectpkg.Manager, *config.Config, string) error,
	runWorktree func(*config.Config, string) error,
) error {
	gitProject, err := classifyProjectDirectory(ctx, cwd)
	if err != nil {
		return fmt.Errorf("classify project directory: %w", err)
	}
	if gitProject {
		if sessionID != "" {
			return errors.New("--session is unavailable in Git worktree mode; use /select to choose a worktree")
		}
		if _, err := prepareLogicalProject(ctx, cwd, cfg); err != nil {
			return fmt.Errorf("open project: %w", err)
		}
		return runWorktree(cfg, cwd)
	}
	proj, err := prepareLogicalProject(ctx, cwd, cfg)
	if err != nil {
		return fmt.Errorf("open project: %w", err)
	}
	return runProject(projectpkg.NewManager(proj, manager), cfg, sessionID)
}

func init() {
	tuiCmd.Flags().StringVarP(&tuiSessionID, "session", "s", "", "session ID to use (defaults to current session)")
	rootCmd.AddCommand(tuiCmd)
}
