package main

import (
	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/setup"
)

// createClient creates a provider client based on configuration
func createClient(cfg *config.Config) (providers.Client, error) {
	return setup.CreateProviderClient(cfg)
}
