package sandbox

// Config is the top-level configuration for sandbox
type Config struct {
	Permissions PermissionsConfig `json:"permissions" yaml:"permissions"`
	Sandbox     SandboxConfig     `json:"sandbox" yaml:"sandbox"`
}

// PermissionsConfig defines allow/deny rules for commands
type PermissionsConfig struct {
	Allow []string `json:"allow" yaml:"allow"`
	Deny  []string `json:"deny" yaml:"deny"`
}

// SandboxConfig defines sandbox isolation settings
type SandboxConfig struct {
	Enabled    bool             `json:"enabled" yaml:"enabled"`
	Filesystem FilesystemConfig `json:"filesystem" yaml:"filesystem"`
	Network    NetworkConfig    `json:"network" yaml:"network"`
	Defaults   DefaultsConfig   `json:"defaults" yaml:"defaults"`
}

// FilesystemConfig defines filesystem access control
type FilesystemConfig struct {
	// ReadOnly paths are mounted as read-only
	ReadOnly []string `json:"readonly" yaml:"readonly"`
	// Deny paths are completely blocked
	Deny []string `json:"deny" yaml:"deny"`
	// Write paths are allowed for writing
	Write []string `json:"write" yaml:"write"`
	// Protected paths cannot be written even if in Write paths
	Protected []string `json:"protected" yaml:"protected"`
}

// NetworkConfig defines network access control
type NetworkConfig struct {
	Isolation bool     `json:"isolation" yaml:"isolation"`
	Allow     []string `json:"allow" yaml:"allow"`
}

// DefaultsConfig defines default execution parameters
type DefaultsConfig struct {
	Timeout string `json:"timeout" yaml:"timeout"` // e.g. "5m"
}

// LoadConfig loads sandbox configuration from file
func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		return cfg, nil
	}

	// Try to load from file
	// TODO: implement file loading with JSON/YAML support
	return cfg, nil
}
