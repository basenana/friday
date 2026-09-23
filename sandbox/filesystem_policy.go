package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type filesystemRuleKind uint8

const (
	filesystemRuleWrite filesystemRuleKind = iota + 1
	filesystemRuleReadOnly
	filesystemRuleProtected
	filesystemRuleDeny
)

type filesystemObjectType uint8

const (
	filesystemObjectFile filesystemObjectType = iota + 1
	filesystemObjectDirectory
	filesystemObjectDevice
)

type filesystemRule struct {
	Path    string
	Kind    filesystemRuleKind
	Object  filesystemObjectType
	Source  string
	Matched string
}

type filesystemPolicy struct {
	Workdir string
	Rules   []filesystemRule
}

// CloneConfig returns an independent sandbox policy. Callers may add
// runtime-scoped filesystem capabilities without mutating the shared
// application configuration used by other agents or dedicated runners.
func CloneConfig(src *Config) *Config {
	if src == nil {
		src = DefaultConfig()
	}
	dst := *src
	dst.Permissions.Allow = append([]string(nil), src.Permissions.Allow...)
	dst.Permissions.Deny = append([]string(nil), src.Permissions.Deny...)
	dst.Sandbox.Filesystem.ReadOnly = append([]string(nil), src.Sandbox.Filesystem.ReadOnly...)
	dst.Sandbox.Filesystem.Deny = append([]string(nil), src.Sandbox.Filesystem.Deny...)
	dst.Sandbox.Filesystem.Write = append([]string(nil), src.Sandbox.Filesystem.Write...)
	dst.Sandbox.Filesystem.Protected = append([]string(nil), src.Sandbox.Filesystem.Protected...)
	dst.Sandbox.Network.Allow = append([]string(nil), src.Sandbox.Network.Allow...)
	return &dst
}

func compileFilesystemPolicy(cfg *Config, workdir, homeDir string) (*filesystemPolicy, error) {
	if cfg == nil {
		return nil, fmt.Errorf("sandbox config is required")
	}
	resolvedWorkdir, err := ValidateWorkdir(workdir)
	if err != nil {
		return nil, err
	}
	policy := &filesystemPolicy{Workdir: resolvedWorkdir}
	byPath := make(map[string]filesystemRule)

	info, err := os.Stat(resolvedWorkdir)
	if err != nil {
		return nil, fmt.Errorf("stat workdir: %w", err)
	}
	byPath[resolvedWorkdir] = filesystemRule{
		Path: resolvedWorkdir, Kind: filesystemRuleWrite, Object: classifyFilesystemObject(info), Source: "workdir", Matched: workdir,
	}

	groups := []struct {
		kind    filesystemRuleKind
		source  string
		entries []string
	}{
		{filesystemRuleWrite, "sandbox.filesystem.write", cfg.Sandbox.Filesystem.Write},
		{filesystemRuleReadOnly, "sandbox.filesystem.readonly", cfg.Sandbox.Filesystem.ReadOnly},
		{filesystemRuleProtected, "sandbox.filesystem.protected", cfg.Sandbox.Filesystem.Protected},
		{filesystemRuleDeny, "sandbox.filesystem.deny", cfg.Sandbox.Filesystem.Deny},
	}
	for _, group := range groups {
		for _, entry := range group.entries {
			matches, err := expandFilesystemRule(entry, resolvedWorkdir, homeDir)
			if err != nil {
				return nil, fmt.Errorf("%s rule %q: %w", group.source, entry, err)
			}
			for _, match := range matches {
				resolved, err := resolveSymlinkedPath(match)
				if err != nil {
					return nil, fmt.Errorf("%s rule %q: %w", group.source, entry, err)
				}
				resolved = filepath.Clean(resolved)
				info, err := os.Stat(resolved)
				if err != nil {
					return nil, fmt.Errorf("%s rule %q: stat %q: %w", group.source, entry, resolved, err)
				}
				object := classifyFilesystemObject(info)
				if object == 0 {
					return nil, fmt.Errorf("%s rule %q: unsupported filesystem object %q", group.source, entry, resolved)
				}
				if group.kind == filesystemRuleDeny && object == filesystemObjectDevice {
					return nil, fmt.Errorf("%s rule %q: deny does not support device nodes", group.source, entry)
				}
				rule := filesystemRule{Path: resolved, Kind: group.kind, Object: object, Source: group.source, Matched: match}
				if current, ok := byPath[resolved]; !ok || filesystemRulePriority(rule.Kind) > filesystemRulePriority(current.Kind) ||
					(filesystemRulePriority(rule.Kind) == filesystemRulePriority(current.Kind) && rule.Kind > current.Kind) {
					byPath[resolved] = rule
				}
			}
		}
	}

	policy.Rules = make([]filesystemRule, 0, len(byPath))
	for _, rule := range byPath {
		policy.Rules = append(policy.Rules, rule)
	}
	sort.Slice(policy.Rules, func(i, j int) bool {
		if policy.Rules[i].Path != policy.Rules[j].Path {
			return policy.Rules[i].Path < policy.Rules[j].Path
		}
		return policy.Rules[i].Kind < policy.Rules[j].Kind
	})
	return policy, nil
}

func expandFilesystemRule(entry, workdir, homeDir string) ([]string, error) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return nil, fmt.Errorf("path is empty")
	}
	if gitPath, ok, err := resolveLinkedWorktreeGitPath(entry, workdir); err != nil {
		return nil, err
	} else if ok {
		entry = gitPath
	}
	if (entry == "~" || strings.HasPrefix(entry, "~/")) && normalizeExecutionHome(homeDir) == "" {
		return nil, fmt.Errorf("HOME is unavailable for %q", entry)
	}
	expanded := filepath.Clean(expandPath(entry, workdir, homeDir))
	if !filepath.IsAbs(expanded) {
		absolute, err := filepath.Abs(expanded)
		if err != nil {
			return nil, err
		}
		expanded = absolute
	}
	if strings.ContainsAny(expanded, "*?[") {
		if _, err := filepath.Match(expanded, expanded); err != nil {
			return nil, fmt.Errorf("invalid glob: %w", err)
		}
		matches, err := filepath.Glob(expanded)
		if err != nil {
			return nil, fmt.Errorf("invalid glob: %w", err)
		}
		return matches, nil
	}
	if _, err := os.Lstat(expanded); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return []string{expanded}, nil
}

// resolveLinkedWorktreeGitPath maps the default .git protection rules to the
// common Git directory when workdir is a linked checkout whose .git is a file.
func resolveLinkedWorktreeGitPath(entry, workdir string) (string, bool, error) {
	if entry != ".git/hooks" && entry != ".git/config" {
		return "", false, nil
	}
	dotGit := filepath.Join(workdir, ".git")
	info, err := os.Stat(dotGit)
	if err != nil || info.IsDir() {
		return "", false, nil
	}
	data, err := os.ReadFile(dotGit)
	if err != nil {
		return "", false, fmt.Errorf("read linked worktree git file: %w", err)
	}
	gitDirValue := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir:"))
	if gitDirValue == "" || gitDirValue == strings.TrimSpace(string(data)) {
		return "", false, fmt.Errorf("invalid linked worktree git file %q", dotGit)
	}
	gitDir := gitDirValue
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(filepath.Dir(dotGit), gitDir)
	}
	commonDir := filepath.Clean(gitDir)
	commonData, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err == nil {
		commonDir = strings.TrimSpace(string(commonData))
		if commonDir == "" {
			return "", false, fmt.Errorf("linked worktree commondir is empty")
		}
		if !filepath.IsAbs(commonDir) {
			commonDir = filepath.Join(gitDir, commonDir)
		}
	} else if !os.IsNotExist(err) {
		return "", false, fmt.Errorf("read linked worktree commondir: %w", err)
	}
	return filepath.Join(filepath.Clean(commonDir), strings.TrimPrefix(entry, ".git/")), true, nil
}

func classifyFilesystemObject(info os.FileInfo) filesystemObjectType {
	switch {
	case info.IsDir():
		return filesystemObjectDirectory
	case info.Mode().IsRegular():
		return filesystemObjectFile
	case info.Mode()&os.ModeDevice != 0:
		return filesystemObjectDevice
	default:
		return 0
	}
}

func filesystemRulePriority(kind filesystemRuleKind) int {
	switch kind {
	case filesystemRuleDeny:
		return 3
	case filesystemRuleReadOnly, filesystemRuleProtected:
		return 2
	case filesystemRuleWrite:
		return 1
	default:
		return 0
	}
}
