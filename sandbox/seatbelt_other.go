//go:build !darwin

package sandbox

// NewSeatbelt returns a no-op sandbox on non-macOS platforms
func NewSeatbelt(cfg *Config) Sandbox {
	return &noopSandbox{}
}
