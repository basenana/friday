package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/spf13/cobra"

	codebasepkg "github.com/basenana/friday/coder/codebase"
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

func openWorktreeService(ctx context.Context, cwd string, cfg *config.Config) (*fridayworktree.Service, error) {
	if cfg == nil {
		return nil, fmt.Errorf("configuration is unavailable")
	}
	if _, err := prepareLogicalProject(ctx, cwd, cfg); err != nil {
		return nil, err
	}
	return fridayworktree.Open(ctx, cwd, cfg.WorktreePath(), cfg.Worktree.BranchPrefix, cfg.DataDirPath())
}

func runWorktreesList(ctx context.Context, cwd string, cfg *config.Config, manager *sessions.Manager, out io.Writer) error {
	service, err := openWorktreeService(ctx, cwd, cfg)
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
		health, err := worktreeSessionHealth(manager, item.SessionID)
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

func worktreeSessionHealth(manager *sessions.Manager, sessionID string) (string, error) {
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
	service, err := openWorktreeService(ctx, cwd, cfg)
	if err != nil {
		return err
	}
	return service.Remove(ctx, id, fridayworktree.RemoveOptions{
		DeleteBranch: deleteBranch,
		Sessions:     manager,
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
