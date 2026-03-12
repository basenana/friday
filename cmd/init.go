package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/basenana/friday/workspace"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize workspace with default files",
	Long:  `Initialize the Friday workspace directory with default markdown files for agent context.`,
	Run: func(cmd *cobra.Command, args []string) {
		ws := workspace.NewWorkspace(cfg.WorkspacePath(), cfg.MemoryPath())

		created, err := ws.Init()
		if err != nil {
			fmt.Printf("failed to init workspace: %v\n", err)
			return
		}

		if len(created) == 0 {
			fmt.Println("Workspace already initialized at:", cfg.WorkspacePath())
			fmt.Println("All files already exist.")
		} else {
			fmt.Println("Workspace initialized at:", cfg.WorkspacePath())
			fmt.Println("")
			fmt.Println("Created files:")
			for _, filename := range created {
				switch filename {
				case "AGENTS.md":
					fmt.Println("  AGENTS.md    - Agent guidelines and memory usage rules")
				case "SOUL.md":
					fmt.Println("  SOUL.md      - Persona, tone, and boundaries")
				case "USER.md":
					fmt.Println("  USER.md      - User info and preferences")
				case "IDENTITY.md":
					fmt.Println("  IDENTITY.md  - Agent name, style, and emoji")
				case "TOOLS.md":
					fmt.Println("  TOOLS.md     - Local tools notes (guidance)")
				case "HEARTBEAT.md":
					fmt.Println("  HEARTBEAT.md - Optional heartbeat checklist")
				case "MEMORY.md":
					fmt.Println("  MEMORY.md    - Long-term memory")
				default:
					fmt.Printf("  %s\n", filename)
				}
			}
			fmt.Println("")
			fmt.Println("Memory directory:", cfg.MemoryPath())
			fmt.Println("")
			fmt.Println("Edit these files to customize your AI assistant's behavior.")
		}
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
