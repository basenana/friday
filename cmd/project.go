package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	projectpkg "github.com/basenana/friday/coder/project"
	"github.com/basenana/friday/config"
	fridayworktree "github.com/basenana/friday/worktree"
)

func classifyProjectDirectory(ctx context.Context, cwd string) (bool, error) {
	marker, err := findGitMarker(cwd)
	if err != nil {
		return false, err
	}
	if marker == "" {
		return false, nil
	}
	if _, err := fridayworktree.DiscoverProjectIdentity(ctx, cwd); err != nil {
		return false, err
	}
	return true, nil
}

func findGitMarker(cwd string) (string, error) {
	current, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(current); resolveErr == nil {
		current = resolved
	}
	if info, err := os.Stat(current); err != nil {
		return "", err
	} else if !info.IsDir() {
		return "", fmt.Errorf("project path is not a directory: %s", current)
	}
	for {
		marker := filepath.Join(current, ".git")
		if _, err := os.Lstat(marker); err == nil {
			return marker, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", nil
		}
		current = parent
	}
}

// prepareLogicalProject opens ordinary directories with their established
// path identity and Git checkouts with their shared common-directory identity.
// A Git open also imports the retained standalone worktree registry before a
// command starts using the project-owned store.
func prepareLogicalProject(ctx context.Context, cwd string, cfg *config.Config) (*projectpkg.Project, error) {
	projectStore := projectpkg.NewFileStore(cfg.ProjectsPath())
	identity, err := fridayworktree.DiscoverProjectIdentity(ctx, cwd)
	if err != nil {
		return projectpkg.Open(cwd, projectStore)
	}
	store, err := fridayworktree.NewStore(cfg.ProjectsPath(), identity.ID)
	if err != nil {
		return nil, err
	}
	legacyPaths, err := fridayworktree.LegacyRegistryPaths(cfg.DataDirPath(), identity)
	if err != nil {
		return nil, err
	}
	for _, legacyPath := range legacyPaths {
		if err := fridayworktree.MigrateLegacy(ctx, legacyPath, store); err != nil {
			return nil, err
		}
	}
	return projectpkg.OpenWithIdentity(cwd, identity, projectStore)
}
