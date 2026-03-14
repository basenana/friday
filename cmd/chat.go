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
	chatVerbose   bool
)

var chatCmd = &cobra.Command{
	Use:   "chat [message]",
	Short: "Send a message to AI assistant",
	Long: `Send a message to the AI assistant and print the response.

Message can be provided as:
  - Command argument: friday chat "hello"
  - Stdin pipe: echo "hello" | friday chat
  - Stdin pipe: cat file.txt | friday chat
  - Combined: cat error.log | friday chat "why is this error?"`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()

		// Get user message from args and/or stdin
		var argMessage, stdinMessage string

		// Read from args
		if len(args) > 0 {
			argMessage = strings.Join(args, "\n")
		}

		// Read from stdin if piped
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to read stdin: %v\n", err)
				os.Exit(1)
			}
			stdinMessage = strings.TrimSpace(string(data))
		}

		// Combine messages
		var userMessage string
		if argMessage != "" {
			userMessage = argMessage
		}
		if stdinMessage != "" {
			if userMessage == "" {
				userMessage = stdinMessage
			} else {
				userMessage = userMessage + "\n\n" + stdinMessage
			}
		}

		userMessage = strings.TrimSpace(userMessage)
		if userMessage == "" {
			fmt.Fprintln(os.Stderr, "Error: no message provided")
			fmt.Fprintln(os.Stderr, "Usage: friday chat \"your message\"")
			fmt.Fprintln(os.Stderr, "   or: echo \"your message\" | friday chat")
			fmt.Fprintln(os.Stderr, "   or: cat file.txt | friday chat \"your question\"")
			os.Exit(1)
		}

		// Setup agent
		var opts []AgentOption
		if chatSessionID != "" {
			opts = append(opts, WithSessionID(chatSessionID))
		}
		if chatVerbose {
			opts = append(opts, WithVerbose(true))
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
	chatCmd.Flags().BoolVarP(&chatVerbose, "verbose", "v", false, "verbose output")
	rootCmd.AddCommand(chatCmd)
}
