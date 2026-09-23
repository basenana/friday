package project

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MigrateIdentity copies the complete checkout-path project directory into a
// stable logical-project directory. The legacy directory remains intact as a
// recovery source. Existing stable files win conflicts, while append-only
// history and sandbox grants are merged so retries are idempotent.
func (s *FileStore) MigrateIdentity(root string, identity Identity) error {
	canonical, err := CanonicalRoot(root)
	if err != nil {
		return err
	}
	if !validID(identity.ID) || strings.TrimSpace(identity.Name) == "" || strings.TrimSpace(identity.Repository) == "" {
		return fmt.Errorf("invalid stable project identity")
	}
	legacyID := ProjectID(canonical)
	if legacyID == identity.ID {
		return nil
	}
	legacyDir := s.projectDir(legacyID)
	if _, err := os.Stat(legacyDir); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}

	// Every migration targeting one logical project takes the stable lock
	// first. This serializes linked checkouts while the legacy lock excludes
	// concurrent writers still using the old checkout identity.
	return s.withLock(identity.ID, func() error {
		return s.withLock(legacyID, func() error {
			return s.migrateIdentityLocked(canonical, legacyID, identity)
		})
	})
}

func (s *FileStore) migrateIdentityLocked(canonical, legacyID string, identity Identity) error {
	legacyDir := s.projectDir(legacyID)
	stableDir := s.projectDir(identity.ID)
	if err := os.MkdirAll(stableDir, 0o700); err != nil {
		return err
	}
	if err := copyLegacyProjectTree(legacyDir, stableDir); err != nil {
		return err
	}
	if err := mergeJSONLLines(filepath.Join(legacyDir, "user_history.jsonl"), filepath.Join(stableDir, "user_history.jsonl")); err != nil {
		return fmt.Errorf("migrate project history: %w", err)
	}
	if err := mergeSandboxAllow(filepath.Join(legacyDir, "sandbox.json"), filepath.Join(stableDir, "sandbox.json")); err != nil {
		return fmt.Errorf("migrate sandbox grants: %w", err)
	}

	now := time.Now()
	meta := Metadata{Version: 2, ID: identity.ID, Name: identity.Name, Root: canonical, Repository: identity.Repository, CreatedAt: now, UpdatedAt: now}
	if legacy, err := readMigrationMetadata(filepath.Join(legacyDir, "project.json")); err == nil {
		meta.CodebaseEnabled = legacy.CodebaseEnabled
		if !legacy.CreatedAt.IsZero() {
			meta.CreatedAt = legacy.CreatedAt
		}
	}
	if stable, err := readMigrationMetadata(filepath.Join(stableDir, "project.json")); err == nil {
		meta.CodebaseEnabled = meta.CodebaseEnabled || stable.CodebaseEnabled
		if !stable.CreatedAt.IsZero() && (meta.CreatedAt.IsZero() || stable.CreatedAt.Before(meta.CreatedAt)) {
			meta.CreatedAt = stable.CreatedAt
		}
	}
	if err := writeAtomicJSON(filepath.Join(stableDir, "project.json"), meta, 0o600); err != nil {
		return fmt.Errorf("write stable project metadata: %w", err)
	}
	marker := filepath.Join(legacyDir, ".migrated-to-"+identity.ID)
	return writeAtomic(marker, []byte(identity.ID+"\n"), 0o600)
}

func copyLegacyProjectTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || rel == "." {
			return err
		}
		base := filepath.Base(path)
		if base == ".lock" || strings.HasPrefix(base, ".migrated-to-") || rel == "project.json" || rel == "user_history.jsonl" || rel == "sandbox.json" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil
		}
		if _, err := os.Stat(target); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return writeAtomic(target, data, info.Mode().Perm())
	})
}

func readMigrationMetadata(path string) (Metadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Metadata{}, err
	}
	var meta Metadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return Metadata{}, err
	}
	return meta, nil
}

func mergeJSONLLines(src, dst string) error {
	source, err := os.ReadFile(src)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	destination, err := os.ReadFile(dst)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	seen := make(map[string]bool)
	lines := make([]string, 0)
	for _, data := range [][]byte{destination, source} {
		for _, line := range bytes.Split(data, []byte{'\n'}) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 || seen[string(line)] {
				continue
			}
			seen[string(line)] = true
			lines = append(lines, string(line))
		}
	}
	if len(lines) == 0 {
		return nil
	}
	return writeAtomic(dst, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

type migrationSandboxAllow struct {
	Version int      `json:"version"`
	Allow   []string `json:"allow"`
}

func mergeSandboxAllow(src, dst string) error {
	read := func(path string) (migrationSandboxAllow, error) {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			return migrationSandboxAllow{}, nil
		}
		if err != nil {
			return migrationSandboxAllow{}, err
		}
		var doc migrationSandboxAllow
		if err := json.Unmarshal(data, &doc); err != nil {
			return migrationSandboxAllow{}, err
		}
		if doc.Version != 1 {
			return migrationSandboxAllow{}, fmt.Errorf("unsupported sandbox grant version %d", doc.Version)
		}
		return doc, nil
	}
	legacy, err := read(src)
	if err != nil {
		return err
	}
	if legacy.Version == 0 {
		return nil
	}
	stable, err := read(dst)
	if err != nil {
		return err
	}
	seen := make(map[string]bool)
	allow := make([]string, 0, len(stable.Allow)+len(legacy.Allow))
	for _, entries := range [][]string{stable.Allow, legacy.Allow} {
		for _, entry := range entries {
			entry = strings.TrimSpace(entry)
			if entry != "" && !seen[entry] {
				seen[entry] = true
				allow = append(allow, entry)
			}
		}
	}
	sort.Strings(allow)
	return writeAtomicJSON(dst, migrationSandboxAllow{Version: 1, Allow: allow}, 0o600)
}
