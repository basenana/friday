package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
	currentTTY   string
)

func getTTYName() string {
	// 1. Environment variable takes precedence
	if envTTY := os.Getenv("FRIDAY_TTY"); envTTY != "" {
		return envTTY
	}

	// 2. Get TTY name via tty command
	cmd := exec.Command("tty")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	ttyPath := strings.TrimSpace(string(output))
	if ttyPath == "" || ttyPath == "not a tty" {
		return ""
	}

	// /dev/ttys000 -> ttys000, /dev/pts/0 -> pts_0
	ttyName := strings.TrimPrefix(ttyPath, "/dev/")
	ttyName = strings.ReplaceAll(ttyName, "/", "_")
	return ttyName
}

func currentFilePath(cfg *config.Config) string {
	// 1. Environment variable takes precedence
	if envPath := os.Getenv("FRIDAY_CURRENT_FILE"); envPath != "" {
		return envPath
	}

	// 2. Based on tty name
	if currentTTY != "" {
		return filepath.Join(cfg.DataDirPath(), "current_"+currentTTY)
	}

	// 3. Fallback to default
	return filepath.Join(cfg.DataDirPath(), "current")
}

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

		// Get TTY name for session isolation
		currentTTY = getTTYName()

		// Initialize session manager with tty-aware current file
		store := file.NewFileSessionStore(cfg.SessionsPath())
		sessMgr = sessions.NewManager(store, currentFilePath(cfg), currentTTY)

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
