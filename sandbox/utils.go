package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func defaultExecutionHome() string {
	home, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(home) != "" {
		return home
	}
	return strings.TrimSpace(os.Getenv("HOME"))
}

func normalizeExecutionHome(homeDir string) string {
	homeDir = strings.TrimSpace(homeDir)
	if homeDir != "" {
		return homeDir
	}
	return defaultExecutionHome()
}

func resolveExecutionHome(homeDir string, env []string) (string, error) {
	home := strings.TrimSpace(homeDir)
	for i := len(env) - 1; i >= 0; i-- {
		if envKey(env[i]) == "HOME" {
			home = strings.TrimSpace(strings.TrimPrefix(env[i], "HOME="))
			if home == "" {
				return "", fmt.Errorf("execution HOME must not be empty")
			}
			break
		}
	}
	if home == "" {
		home = defaultExecutionHome()
	}
	if home == "" {
		return "", fmt.Errorf("execution HOME is unavailable")
	}
	if !filepath.IsAbs(home) {
		return "", fmt.Errorf("execution HOME must be absolute: %q", home)
	}
	return home, nil
}

// expandPath expands ~ and relative paths. homeDir overrides the home used
// for ~ expansion; when empty it falls back to the process home directory.
func expandPath(path, workdir, homeDir string) string {
	// Expand ~ to home directory
	if path == "~" || strings.HasPrefix(path, "~/") {
		home := normalizeExecutionHome(homeDir)
		if home == "" {
			return path
		}
		if path == "~" {
			return home
		}
		return filepath.Join(home, path[2:])
	}

	// Expand relative paths using workdir
	if workdir != "" && !filepath.IsAbs(path) {
		return filepath.Join(workdir, path)
	}

	return path
}

// parseMemoryLimit parses memory limit string like "2G", "512M", "1024K" (case insensitive)
func parseMemoryLimit(limit string) int64 {
	if limit == "" {
		return 0
	}

	limit = strings.TrimSpace(limit)
	if len(limit) == 0 {
		return 0
	}

	// Convert to uppercase for consistent matching
	upperLimit := strings.ToUpper(limit)

	// Extract number and unit
	var numStr string
	var multiplier int64 = 1

	// Check last character for unit suffix
	lastChar := upperLimit[len(upperLimit)-1]
	switch lastChar {
	case 'G':
		multiplier = 1024 * 1024 * 1024
		numStr = limit[:len(limit)-1]
	case 'M':
		multiplier = 1024 * 1024
		numStr = limit[:len(limit)-1]
	case 'K':
		multiplier = 1024
		numStr = limit[:len(limit)-1]
	default:
		numStr = limit
	}

	num, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		return 0
	}

	return num * multiplier
}
