package main

import (
	"github.com/spf13/cobra"

	"github.com/basenana/friday/tui"
)

var tuiSessionID string

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Start interactive TUI chat session (like claude code / codex)",
	Long: `Launch a full-screen interactive TUI for multi-turn conversations.

Features:
  - Streaming markdown-rendered responses
  - Reasoning blocks (dim, collapsible)
  - Tool call visualization (bordered boxes)
  - Slash commands: /clear /new /quit /help
  - Ctrl+C: cancel current run or quit when idle`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return tui.Run(sessMgr, cfg, tuiSessionID)
	},
}

func init() {
	tuiCmd.Flags().StringVarP(&tuiSessionID, "session", "s", "", "session ID to use (defaults to current session)")
	rootCmd.AddCommand(tuiCmd)
}
