package config

import "strings"

const defaultDecisionBaseURL = "https://openrouter.ai/api/v1"

func (m DecisionModelConfig) IsConfigured() bool {
	return strings.TrimSpace(m.Model) != ""
}

func (m DecisionModelConfig) EffectiveProvider() string {
	provider := strings.ToLower(strings.TrimSpace(m.Provider))
	if provider == "" {
		return "openrouter"
	}
	return provider
}

func (m DecisionModelConfig) EffectiveBaseURL() string {
	if baseURL := strings.TrimSpace(m.BaseURL); baseURL != "" {
		return baseURL
	}
	return defaultDecisionBaseURL
}
