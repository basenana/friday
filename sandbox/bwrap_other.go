//go:build !linux

package sandbox

// NewBwrap returns a no-op sandbox on non-Linux platforms
func NewBwrap(cfg *Config) Sandbox {
	return &noopSandbox{}
}
