//go:build darwin

package sandbox

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/basenana/friday/shellcmd"
)

//go:embed seatbelt_base.sb
var seatbeltBasePolicy string

//go:embed seatbelt_platform.sb
var seatbeltPlatformPolicy string

//go:embed seatbelt_network.sb
var seatbeltNetworkPolicy string

// Seatbelt implements Sandbox using macOS sandbox-exec.
type Seatbelt struct {
	config *Config
	binary string

	probeOnce sync.Once
	probeOK   bool
}

func NewSeatbelt(cfg *Config) *Seatbelt {
	binary, _ := exec.LookPath("sandbox-exec")
	if binary != "" {
		if absolute, err := filepath.Abs(binary); err == nil {
			binary = absolute
		}
		if physical, err := filepath.EvalSymlinks(binary); err == nil {
			binary = physical
		}
	}
	return &Seatbelt{config: cfg, binary: binary}
}

func (s *Seatbelt) WrapCommand(cmd string, opts ExecOptions) (string, func(), error) {
	if !s.config.Sandbox.Enabled {
		return cmd, func() {}, nil
	}
	if s.binary == "" {
		return "", nil, fmt.Errorf("sandbox-exec is unavailable")
	}
	profile, err := s.generateProfile(opts.Workdir, opts.HomeDir)
	if err != nil {
		return "", nil, err
	}
	tmpFile, err := os.CreateTemp("", "friday-sandbox-*.sb")
	if err != nil {
		return "", nil, fmt.Errorf("create Seatbelt profile: %w", err)
	}
	profilePath := tmpFile.Name()
	cleanup := func() { _ = os.Remove(profilePath) }
	if _, err := tmpFile.WriteString(profile); err != nil {
		_ = tmpFile.Close()
		cleanup()
		return "", nil, fmt.Errorf("write Seatbelt profile: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("close Seatbelt profile: %w", err)
	}
	return shellcmd.Join(s.binary, "-f", profilePath, "--", "bash", "-c", cmd), cleanup, nil
}

// IsAvailable memoizes a probe that executes the complete production profile.
func (s *Seatbelt) IsAvailable() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	if s.binary == "" {
		return false
	}
	s.probeOnce.Do(func() {
		workdir, err := os.MkdirTemp("", "friday-seatbelt-probe-*")
		if err != nil {
			return
		}
		defer os.RemoveAll(workdir)
		profile, err := s.generateProfile(workdir, workdir)
		if err != nil {
			return
		}
		trueBin := findTrueBinary()
		if trueBin == "" {
			return
		}
		s.probeOK = exec.Command(s.binary, "-p", profile, "--", trueBin).Run() == nil
	})
	return s.probeOK
}

func findTrueBinary() string {
	if binary, err := exec.LookPath("true"); err == nil {
		return binary
	}
	for _, candidate := range []string{"/usr/bin/true", "/bin/true"} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func (s *Seatbelt) Name() string { return "seatbelt" }

func (s *Seatbelt) generateProfile(workdir, homeDir string) (string, error) {
	policy, err := compileFilesystemPolicy(s.config, workdir, homeDir)
	if err != nil {
		return "", err
	}
	var profile strings.Builder
	profile.WriteString(seatbeltBasePolicy)
	profile.WriteByte('\n')
	profile.WriteString(seatbeltPlatformPolicy)
	profile.WriteByte('\n')

	for _, rule := range policy.Rules {
		switch rule.Kind {
		case filesystemRuleWrite:
			writeSeatbeltConcreteRule(&profile, "allow", "file-write*", rule.Path)
		case filesystemRuleReadOnly, filesystemRuleProtected:
			writeSeatbeltConcreteRule(&profile, "deny", "file-write*", rule.Path)
		case filesystemRuleDeny:
			writeSeatbeltConcreteRule(&profile, "deny", "file-read* file-write*", rule.Path)
		}
	}
	if !s.config.Sandbox.Network.Isolation {
		profile.WriteString(seatbeltNetworkPolicy)
		profile.WriteByte('\n')
	}
	return profile.String(), nil
}

func writeSeatbeltConcreteRule(profile *strings.Builder, decision, operations, path string) {
	fmt.Fprintf(profile, "(%s %s (subpath %q))\n", decision, operations, path)
}
