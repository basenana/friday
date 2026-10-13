package config

import (
	"strings"
	"time"
)

const defaultDecisionBaseURL = "https://openrouter.ai/api/v1"

func (m DecisionModelConfig) IsConfigured() bool {
	return strings.TrimSpace(m.Model) != ""
}

func (m DecisionModelConfig) EffectiveProvider() string {
	provider := strings.ToLower(strings.TrimSpace(m.Provider))
	if provider == "" {
		return "jev"
	}
	return provider
}

func (m DecisionModelConfig) EffectiveBaseURL() string {
	if baseURL := strings.TrimSpace(m.BaseURL); baseURL != "" {
		return baseURL
	}
	return defaultDecisionBaseURL
}

// TimeoutDuration returns the decision-model request timeout. Zero means
// "use the provider default (15m)". Unparseable values fall back to zero;
// validate() already rejects them at load time.
func (m DecisionModelConfig) TimeoutDuration() time.Duration {
	d, err := parseTimeoutString(m.Timeout, "decision_model.timeout")
	if err != nil {
		return 0
	}
	return d
}
