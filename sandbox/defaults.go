package sandbox

// DefaultProtectedPaths are paths that are always protected from writing
var DefaultProtectedPaths = []string{
	"~/.ssh",
	"~/.aws",
	"~/.gnupg",
	"~/.bashrc",
	"~/.bash_profile",
	"~/.zshrc",
	"~/.zprofile",
	"~/.profile",
	"~/.gitconfig",
	"~/.gitmodules",
	".git/hooks",
	".git/config",
	".vscode",
	".idea",
	".env",
	"*.pem",
	"*.key",
}

// DefaultDeniedCommands are commands that are denied by default
var DefaultDeniedCommands = []string{
	"sudo",
	"su",
}

// DefaultAllowedDomains are domains allowed by default for network access
var DefaultAllowedDomains = []string{
	"github.com",
	"*.github.com",
	"api.github.com",
	"registry.npmjs.org",
	"proxy.golang.org",
	"pkg.go.dev",
}

// DefaultAllowedCommands are commands allowed by default
var DefaultAllowedCommands = []string{
	"friday",
	"git",
	"npm",
	"yarn",
	"pnpm",
	"make",
	"go",
	"cargo",
	"rustc",
	"python",
	"python3",
	"node",
	"echo",
	"cat",
	"ls",
	"pwd",
	"mkdir",
	"touch",
	"cp",
	"mv",
	"rm",
	"grep",
	"find",
	"sed",
	"awk",
	"head",
	"tail",
	"wc",
	"sort",
	"uniq",
	"diff",
	"tar",
	"unzip",
	"curl",
	"wget",
}

// DefaultConfig returns the default sandbox configuration
func DefaultConfig() *Config {
	return &Config{
		Permissions: PermissionsConfig{
			Allow: append([]string{}, DefaultAllowedCommands...),
			Deny:  append([]string{}, DefaultDeniedCommands...),
		},
		Sandbox: SandboxConfig{
			Enabled: true,
			Filesystem: FilesystemConfig{
				ReadOnly:  []string{},
				Deny:      append([]string{}, DefaultProtectedPaths[:3]...), // ~/.ssh, ~/.aws, ~/.gnupg
				Write:     []string{".", "/tmp"},
				Protected: append([]string{}, DefaultProtectedPaths...),
			},
			Network: NetworkConfig{
				Enabled: true,
				Allow:   append([]string{}, DefaultAllowedDomains...),
			},
			Defaults: DefaultsConfig{
				Timeout:     "5m",
				MemoryLimit: "2G",
			},
		},
	}
}
