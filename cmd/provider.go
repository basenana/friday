package main

import (
	"fmt"
	"strings"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/providers/anthropics"
	"github.com/basenana/friday/core/providers/openai"
)

// createClient creates a provider client based on configuration
func createClient(cfg *config.Config) (providers.Client, error) {
	provider := strings.ToLower(cfg.Model.Provider)

	switch provider {
	case "anthropic":
		host := cfg.Model.BaseURL
		if host == "" {
			host = "https://api.anthropic.com"
		}
		temp := cfg.Model.Temperature
		maxTokens := int64(cfg.Model.MaxTokens)
		return anthropics.New(host, cfg.Model.Key, anthropics.Model{
			Name:        cfg.Model.Model,
			Temperature: &temp,
			MaxTokens:   &maxTokens,
			QPM:         cfg.Model.QPM,
			Proxy:       cfg.Model.Proxy,
		}), nil
	case "openai", "":
		host := cfg.Model.BaseURL
		if host == "" {
			host = "https://api.openai.com/v1"
		}
		temp := cfg.Model.Temperature
		return openai.New(host, cfg.Model.Key, openai.Model{
			Name:        cfg.Model.Model,
			Temperature: &temp,
			QPM:         cfg.Model.QPM,
		}), nil
	default:
		return nil, fmt.Errorf("unknown provider: %s", provider)
	}
}
