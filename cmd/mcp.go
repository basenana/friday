package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	fridaymcp "github.com/basenana/friday/mcp"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{Use: "mcp", Short: "Manage MCP servers"}

func commandMCPManager() (*fridaymcp.Manager, error) {
	ws := configuredWorkspace(cfg)
	root, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return fridaymcp.NewManager(fridaymcp.ManagerConfig{
		ConfigRoots: ws.MCPRoots(), ProjectRoot: root,
		CacheRoot: cfg.CachesPath(), TrustPath: filepath.Join(cfg.StatePath(), "mcp_trust.json"),
	})
}

func printMCPStatus(statuses []fridaymcp.ServerStatus) {
	if len(statuses) == 0 {
		fmt.Println("No MCP servers configured")
		return
	}
	for _, status := range statuses {
		line := fmt.Sprintf("%-24s %-15s %-15s tools=%d", status.Name, status.Transport, status.State, status.Tools)
		if status.Error != "" {
			line += " error=" + status.Error
		}
		fmt.Println(line)
	}
}

func withMCP(run func(context.Context, *fridaymcp.Manager) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		manager, err := commandMCPManager()
		if err != nil {
			return err
		}
		defer manager.Close()
		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()
		return run(ctx, manager)
	}
}

var mcpListCmd = &cobra.Command{Use: "list", Short: "List configured MCP servers", Args: cobra.NoArgs, RunE: withMCP(func(_ context.Context, m *fridaymcp.Manager) error { printMCPStatus(m.Status()); return nil })}
var mcpInspectCmd = &cobra.Command{Use: "inspect <server>", Short: "Show MCP server status", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
	m, err := commandMCPManager()
	if err != nil {
		return err
	}
	defer m.Close()
	status, err := m.Inspect(args[0])
	if err != nil {
		return err
	}
	data, _ := json.MarshalIndent(status, "", "  ")
	fmt.Println(string(data))
	return nil
}}
var mcpTestCmd = &cobra.Command{Use: "test <server>", Short: "Connect and list tools", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
	m, err := commandMCPManager()
	if err != nil {
		return err
	}
	defer m.Close()
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	if err = m.Reconnect(ctx, args[0]); err != nil {
		return err
	}
	status, _ := m.Inspect(args[0])
	printMCPStatus([]fridaymcp.ServerStatus{status})
	return nil
}}
var mcpRefreshCmd = &cobra.Command{Use: "refresh [server]", Short: "Refresh MCP tool definitions", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
	m, err := commandMCPManager()
	if err != nil {
		return err
	}
	defer m.Close()
	name := ""
	if len(args) > 0 {
		name = args[0]
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	if err = m.Refresh(ctx, name); err != nil {
		return err
	}
	printMCPStatus(m.Status())
	return nil
}}
var mcpReconnectCmd = &cobra.Command{Use: "reconnect <server>", Short: "Reconnect an MCP server", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
	m, err := commandMCPManager()
	if err != nil {
		return err
	}
	defer m.Close()
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	if err = m.Reconnect(ctx, args[0]); err != nil {
		return err
	}
	status, _ := m.Inspect(args[0])
	printMCPStatus([]fridaymcp.ServerStatus{status})
	return nil
}}
var mcpTrustCmd = &cobra.Command{Use: "trust <server>", Short: "Trust the current project MCP server configuration", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
	m, err := commandMCPManager()
	if err != nil {
		return err
	}
	defer m.Close()
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	if err = m.Trust(ctx, args[0]); err != nil {
		return err
	}
	fmt.Printf("Trusted MCP server %s\n", args[0])
	return nil
}}
var mcpUntrustCmd = &cobra.Command{Use: "untrust <server>", Short: "Revoke trust for a project MCP server", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
	m, err := commandMCPManager()
	if err != nil {
		return err
	}
	defer m.Close()
	if err = m.Untrust(cmd.Context(), args[0]); err != nil {
		return err
	}
	fmt.Printf("Untrusted MCP server %s\n", args[0])
	return nil
}}

func init() {
	mcpCmd.AddCommand(mcpListCmd, mcpInspectCmd, mcpTestCmd, mcpRefreshCmd, mcpReconnectCmd, mcpTrustCmd, mcpUntrustCmd)
	rootCmd.AddCommand(mcpCmd)
}
