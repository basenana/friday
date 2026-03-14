package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/sessions"
	"github.com/basenana/friday/sessions/file"
	"github.com/basenana/friday/utils/logger"
)

var (
	cfgFile      string
	workspaceDir string
	cfg          *config.Config
	sessMgr      *sessions.Manager
)

var rootCmd = &cobra.Command{
	Use:   "friday",
	Short: "A Unix-philosophy AI Agent for your terminal",
	Long: `Friday - A Unix-philosophy AI Agent for your terminal.

Text in, text out. Pipe-friendly. No GUI, no cloud dependency.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		var err error
		cfg, err = config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		if workspaceDir != "" {
			cfg.Workspace = workspaceDir
		}

		// Initialize session manager
		store := file.NewFileSessionStore(cfg.SessionsPath())
		currentFile := filepath.Join(cfg.DataDirPath(), "current")
		sessMgr = sessions.NewManager(store, currentFile)

		return nil
	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		logger.Sync()
		logger.Close()
	},
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "config file path")
	rootCmd.PersistentFlags().StringVarP(&workspaceDir, "workspace", "w", "", "workspace directory")
}
