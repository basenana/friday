package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var heartbeatCmd = &cobra.Command{
	Use:   "heartbeat",
	Short: "Send heartbeat message to AI assistant",
	Long:  `Send the HEARTBEAT.md content as a message to the AI assistant and get a response.`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()

		// Setup agent with verbose output
		agentCtx, err := SetupAgent(ctx, cfg, sessMgr, WithVerbose(true))
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}

		// Load HEARTBEAT.md content
		heartbeatContent, err := agentCtx.Workspace.LoadFile("HEARTBEAT.md")
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to load HEARTBEAT.md: %v\n", err)
			os.Exit(1)
		}

		heartbeatContent = strings.TrimSpace(heartbeatContent)
		if heartbeatContent == "" {
			fmt.Println("HEARTBEAT.md is empty, nothing to send.")
			return
		}

		fmt.Println("Sending heartbeat...")
		fmt.Println()

		// Send message and print response
		resp := agentCtx.Chat(ctx, heartbeatContent)
		PrintResponse(resp)
	},
}

func init() {
	rootCmd.AddCommand(heartbeatCmd)
}
