package main

import (
	"fmt"
	"os"

	"github.com/basenana/friday/config"
	corelogger "github.com/basenana/friday/core/logger"
	"github.com/basenana/friday/utils/logger"
)

func main() {
	logger.InitWithFile(config.LogPath(), cfg.Log.MaxDays)
	defer logger.Close()

	// Set core logger root to use our logger
	corelogger.SetRoot(logger.CoreLogger())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
