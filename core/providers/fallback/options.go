package fallback

// fallbackConfig holds the resolved options for a FallbackClient.
type fallbackConfig struct {
	maxTotalRetries int
}

func defaultConfig() fallbackConfig {
	return fallbackConfig{
		maxTotalRetries: 0, // 0 means try every candidate once
	}
}

// FallbackOption configures a FallbackClient.
type FallbackOption func(*fallbackConfig)

// WithMaxTotalRetries limits how many distinct candidates are tried. It is
// retained for compatibility; candidates are never revisited.
func WithMaxTotalRetries(n int) FallbackOption {
	return func(cfg *fallbackConfig) {
		cfg.maxTotalRetries = n
	}
}
