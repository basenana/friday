package main

import (
	"fmt"
	"os"

	"git.hypo.xyz/agents/friday/config"
	corelogger "git.hypo.xyz/agents/friday/core/logger"
	"git.hypo.xyz/agents/friday/utils/logger"
)

func main() {
	// Initialize with default config first, will be updated after config is loaded
	logger.InitWithFile(config.LogPath())
	defer logger.Close()
	logOuterSandboxWarning()

	// Set core logger root to use our logger
	corelogger.SetRoot(logger.CoreLogger())

	if err := rootCmd.Execute(); err != nil {
		corelogger.New("cmd").Errorw("command failed", "error", err)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

const outerSandboxWarning = "IS_SANDBOX=1 detected; all Friday sandbox protections are disabled"

func logOuterSandboxWarning() {
	if os.Getenv("IS_SANDBOX") != "1" || !logger.IsFileBacked() {
		return
	}
	logger.New("cmd").Warn(outerSandboxWarning)
}
