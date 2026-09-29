package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/spf13/cobra"

	codebasepkg "github.com/basenana/friday/coder/codebase"
	projectpkg "github.com/basenana/friday/coder/project"
	"github.com/basenana/friday/config"
	"github.com/basenana/friday/sessions"
	fridayworktree "github.com/basenana/friday/worktree"
)

var worktreesRemoveDeleteBranch bool

var worktreesCmd = &cobra.Command{
	Use:   "worktrees",
	Short: "Manage project worktrees",
}

var worktreesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List project worktrees",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get project directory: %w", err)
		}
		return runWorktreesList(cmd.Context(), cwd, cfg, sessMgr, cmd.OutOrStdout())
	},
}

var worktreesRemoveCmd = &cobra.Command{
	Use:   "remove <id>",
	Short: "Remove one project worktree",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get project directory: %w", err)
		}
		return runWorktreesRemove(cmd.Context(), cwd, cfg, sessMgr, args[0], worktreesRemoveDeleteBranch)
	},
}

// openWorktreeService opens the logical project and its worktree store, and
// adopts legacy one-session-per-worktree references into the project session
// pool. Adoption is idempotent, so every entrypoint can call it.
func openWorktreeService(ctx context.Context, cwd string, cfg *config.Config, manager *sessions.Manager) (*fridayworktree.Service, fridayworktree.Store, *projectpkg.Project, error) {
	if cfg == nil {
		return nil, nil, nil, fmt.Errorf("configuration is unavailable")
	}
	project, err := prepareLogicalProject(ctx, cwd, cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	service, err := fridayworktree.Open(ctx, cwd, cfg.WorktreePath(), cfg.Worktree.BranchPrefix, cfg.DataDirPath())
	if err != nil {
		return nil, nil, nil, err
	}
	store, err := fridayworktree.NewStore(cfg.ProjectsPath(), service.ProjectIdentity().ID)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := fridayworktree.AdoptSessions(ctx, store, service.ProjectCodeRoot(), project, manager); err != nil {
		return nil, nil, nil, fmt.Errorf("adopt legacy worktree sessions: %w", err)
	}
	return service, store, project, nil
}

func runWorktreesList(ctx context.Context, cwd string, cfg *config.Config, manager *sessions.Manager, out io.Writer) error {
	service, _, project, err := openWorktreeService(ctx, cwd, cfg, manager)
	if err != nil {
		return err
	}
	items, err := service.List(ctx)
	if err != nil {
		return err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	if _, err := fmt.Fprintln(out, "ID\tNAME\tBRANCH\tPATH\tSESSION\tCURRENT\tSTALE"); err != nil {
		return err
	}
	for _, item := range items {
		sessionID, err := project.CurrentSessionID(worktreeScope(service, item))
		if err != nil {
			return fmt.Errorf("inspect session for worktree %s: %w", item.ID, err)
		}
		health, err := sessionHealth(manager, sessionID)
		if err != nil {
			return fmt.Errorf("inspect session for worktree %s: %w", item.ID, err)
		}
		branch := item.Branch
		if branch == "" {
			branch = "-"
		}
		if _, err := fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			item.ID, item.Name, branch, item.Path, health, yesMarker(item.Current), yesMarker(item.Stale)); err != nil {
			return err
		}
	}
	return nil
}

// worktreeScope resolves the session scope of one worktree through the single
// shared rule: the main checkout uses the project scope, every linked checkout
// uses its own.
func worktreeScope(service *fridayworktree.Service, item fridayworktree.Worktree) string {
	return fridayworktree.ScopeForWorktree(
		fridayworktree.Metadata{ID: item.ID, Name: item.Name, Path: item.Path}, service.ProjectCodeRoot())
}

func sessionHealth(manager *sessions.Manager, sessionID string) (string, error) {
	if sessionID == "" {
		return "none", nil
	}
	if manager == nil {
		return "unknown", nil
	}
	exists, err := manager.Exists(sessionID)
	if err != nil {
		return "", err
	}
	if !exists {
		return "missing", nil
	}
	active, err := manager.IsActive(sessionID)
	if err != nil {
		return "", err
	}
	if !active {
		return "archived", nil
	}
	return "healthy", nil
}

func yesMarker(value bool) string {
	if value {
		return "yes"
	}
	return "-"
}

func runWorktreesRemove(ctx context.Context, cwd string, cfg *config.Config, manager *sessions.Manager, id string, deleteBranch bool) error {
	service, store, project, err := openWorktreeService(ctx, cwd, cfg, manager)
	if err != nil {
		return err
	}
	// The id may name a checkout that is already gone, so scope resolution runs
	// from stored metadata instead of live Git state.
	meta, err := store.Get(id)
	if err != nil {
		return err
	}
	pool := projectpkg.NewScopedManager(project, manager, fridayworktree.ScopeForWorktree(meta, service.ProjectCodeRoot()))
	return service.Remove(ctx, id, fridayworktree.RemoveOptions{
		DeleteBranch: deleteBranch,
		Sessions:     pool,
		AcquireProjectLock: func() (func(), error) {
			return codebasepkg.AcquireProjectLock(cfg.DataDirPath(), service.ProjectIdentity().ID, cwd)
		},
	})
}

func init() {
	worktreesRemoveCmd.Flags().BoolVar(&worktreesRemoveDeleteBranch, "delete-branch", false, "safely delete the branch after removing the checkout")
	worktreesCmd.AddCommand(worktreesListCmd, worktreesRemoveCmd)
	rootCmd.AddCommand(worktreesCmd)
}
