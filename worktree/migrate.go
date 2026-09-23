package worktree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MigrateLegacy imports a repository's retained version-1 registry into its
// project-owned worktree store. Session IDs remain soft references; migration
// never reads, creates, or copies session entities.
func MigrateLegacy(ctx context.Context, legacyPath string, store Store) error {
	if ctx == nil {
		return fmt.Errorf("migration context is required")
	}
	legacyPath = strings.TrimSpace(legacyPath)
	if legacyPath == "" {
		return fmt.Errorf("legacy registry path is required")
	}
	if store == nil {
		return fmt.Errorf("worktree store is required")
	}
	if _, err := os.Stat(legacyPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat legacy worktree registry: %w", err)
	}

	return withRegistryLock(legacyPath, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		legacy, err := loadRegistry(legacyPath)
		if err != nil {
			return fmt.Errorf("load legacy worktree registry: %w", err)
		}
		if legacy.Migrated {
			return nil
		}
		entries := canonicalMigrationEntries(legacy.Entries)
		for _, meta := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, err := importLegacyMetadata(store, meta); err != nil {
				return fmt.Errorf("migrate worktree %s: %w", meta.Path, err)
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		legacy.Migrated = true
		if err := saveRegistry(legacyPath, legacy); err != nil {
			return fmt.Errorf("record legacy worktree migration: %w", err)
		}
		return nil
	})
}

type legacyMetadataImporter interface {
	importLegacy(Metadata) (Metadata, error)
}

func importLegacyMetadata(store Store, meta Metadata) (Metadata, error) {
	if importer, ok := store.(legacyMetadataImporter); ok {
		return importer.importLegacy(meta)
	}
	return store.Ensure(meta)
}

type migrationEntry struct {
	meta Metadata
	rank time.Time
}

func canonicalMigrationEntries(entries []registryEntry) []Metadata {
	byPath := make(map[string]migrationEntry, len(entries))
	for _, entry := range entries {
		branch := strings.TrimSpace(entry.Branch)
		path, err := canonicalWorktreePath(entry.Path)
		if err != nil || branch == "" {
			continue
		}
		incoming := Metadata{
			Version: 1, ID: worktreeID(path), Name: filepath.Base(path), Path: path, Branch: branch,
			SessionID: strings.TrimSpace(entry.SessionID), CreatedAt: entry.CreatedAt, LastUsedAt: entry.LastUsedAt,
		}
		current, exists := byPath[path]
		if !exists {
			byPath[path] = migrationEntry{meta: incoming, rank: entry.LastUsedAt}
			continue
		}
		current.meta.CreatedAt = earliestNonZero(current.meta.CreatedAt, incoming.CreatedAt)
		if incoming.LastUsedAt.After(current.meta.LastUsedAt) {
			current.meta.LastUsedAt = incoming.LastUsedAt
		}
		if entry.LastUsedAt.After(current.rank) || entry.LastUsedAt.Equal(current.rank) {
			current.meta.Name = incoming.Name
			current.meta.Branch = incoming.Branch
			if incoming.SessionID != "" {
				current.meta.SessionID = incoming.SessionID
			}
			current.rank = entry.LastUsedAt
		}
		byPath[path] = current
	}

	result := make([]Metadata, 0, len(byPath))
	for _, entry := range byPath {
		result = append(result, entry.meta)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result
}

func earliestNonZero(a, b time.Time) time.Time {
	if a.IsZero() {
		return b
	}
	if b.IsZero() || a.Before(b) {
		return a
	}
	return b
}
