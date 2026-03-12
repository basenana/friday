package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var (
	chatSessionID string
	chatMessage   string
)

var chatCmd = &cobra.Command{
	Use:   "chat [message]",
	Short: "Send a message to AI assistant",
	Long: `Send a message to the AI assistant and print the response.

Message can be provided as:
  - Command argument: friday chat "hello"
  - Stdin pipe: echo "hello" | friday chat
  - Stdin pipe: cat file.txt | friday chat
  - Combined: cat error.log | friday chat -m "why is this error?"`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()

		// Get user message from args or stdin
		var userMessage string
		if len(args) > 0 {
			userMessage = strings.Join(args, " ")
		} else {
			stat, _ := os.Stdin.Stat()
			if (stat.Mode() & os.ModeCharDevice) == 0 {
				data, err := io.ReadAll(os.Stdin)
				if err != nil {
					fmt.Fprintf(os.Stderr, "failed to read stdin: %v\n", err)
					os.Exit(1)
				}
				userMessage = strings.TrimSpace(string(data))
			}
		}

		if userMessage == "" && chatMessage == "" {
			fmt.Fprintln(os.Stderr, "Error: no message provided")
			fmt.Fprintln(os.Stderr, "Usage: friday chat \"your message\"")
			fmt.Fprintln(os.Stderr, "   or: echo \"your message\" | friday chat")
			fmt.Fprintln(os.Stderr, "   or: cat file.txt | friday chat -m \"your question\"")
			os.Exit(1)
		}

		// Prepend --message content if provided
		if chatMessage != "" {
			if userMessage != "" {
				userMessage = chatMessage + "\n\n" + userMessage
			} else {
				userMessage = chatMessage
			}
		}

		// Setup agent
		opts := []AgentOption{}
		if chatSessionID != "" {
			opts = append(opts, WithSessionID(chatSessionID))
		}

		agentCtx, err := SetupAgent(ctx, cfg, sessMgr, opts...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}

		// Send message and print response
		resp := agentCtx.Chat(ctx, userMessage)
		PrintResponse(resp)
	},
}

func init() {
	chatCmd.Flags().StringVarP(&chatSessionID, "session", "s", "", "session ID to use")
	chatCmd.Flags().StringVarP(&chatMessage, "message", "m", "", "message to prepend before stdin content")
	rootCmd.AddCommand(chatCmd)
}
