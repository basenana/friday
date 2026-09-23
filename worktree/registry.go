package worktree

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// registry is the legacy global registry format retained for a later migration.
type registry struct {
	Version  int             `json:"version"`
	Entries  []registryEntry `json:"entries"`
	Migrated bool            `json:"migrated,omitempty"`
}

type registryEntry struct {
	Path       string    `json:"path"`
	Branch     string    `json:"branch"`
	ProjectID  string    `json:"project_id,omitempty"`
	SessionID  string    `json:"session_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
}

func loadRegistry(path string) (registry, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return registry{Version: 1}, nil
	}
	if err != nil {
		return registry{}, err
	}
	var result registry
	if err := json.Unmarshal(data, &result); err != nil {
		return registry{}, err
	}
	if result.Version != 1 {
		return registry{}, errors.New("unsupported worktree registry version")
	}
	return result, nil
}

func saveRegistry(path string, r registry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".registry-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
