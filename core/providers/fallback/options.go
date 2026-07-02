package fallback

// fallbackConfig holds the resolved options for a FallbackClient.
type fallbackConfig struct {
	maxTotalRetries int
}

func defaultConfig() fallbackConfig {
	return fallbackConfig{
		maxTotalRetries: 0, // 0 means auto-calculate as len(models) * 3
	}
}

// FallbackOption configures a FallbackClient.
type FallbackOption func(*fallbackConfig)

// WithMaxTotalRetries sets the maximum number of total attempts across all models.
func WithMaxTotalRetries(n int) FallbackOption {
	return func(cfg *fallbackConfig) {
		cfg.maxTotalRetries = n
	}
}
