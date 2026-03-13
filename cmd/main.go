package main

import (
	"fmt"
	"os"

	"github.com/basenana/friday/config"
	corelogger "github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/utils/logger"
)

func main() {
	// Initialize with default config first, will be updated after config is loaded
	defaultCfg := config.DefaultConfig()
	logger.InitWithFile(config.LogPath(), defaultCfg.Log.MaxDays)
	defer logger.Close()

	// Set core logger root to use our logger
	corelogger.SetRoot(logger.CoreLogger())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
