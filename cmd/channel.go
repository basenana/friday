package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/basenana/friday/a2a"
)

var (
	channelListen    string
	channelPublicURL string
	channelAuthToken string
)

var channelCmd = &cobra.Command{
	Use:   "channel",
	Short: "Start an A2A server exposing Friday's chat capability",
	Long: `Start an A2A (Agent-to-Agent) protocol server that exposes Friday's chat
capability through standard A2A interfaces.

The server supports:
  - Agent Card discovery at /.well-known/agent-card.json
  - JSON-RPC 2.0 endpoint for message/send, message/stream, tasks/get, tasks/cancel`,
	Run: func(cmd *cobra.Command, args []string) {
		if channelPublicURL == "" {
			channelPublicURL = "http://" + channelListen + "/"
		}
		// Normalize trailing slash
		if !strings.HasSuffix(channelPublicURL, "/") {
			channelPublicURL += "/"
		}

		server, err := a2a.NewServer(a2a.Config{
			BaseURL: channelPublicURL,
			Listen:  channelListen,
		}, cfg, sessMgr, channelAuthToken)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to create A2A server: %v\n", err)
			os.Exit(1)
		}

		// Handle graceful shutdown
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

		go func() {
			<-sigCh
			fmt.Fprintln(os.Stderr, "\nshutting down...")
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := server.Shutdown(shutdownCtx); err != nil {
				fmt.Fprintf(os.Stderr, "shutdown error: %v\n", err)
			}
		}()

		if err := server.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "server error: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	channelCmd.Flags().StringVar(&channelListen, "listen", "127.0.0.1:8999", "address to listen on")
	channelCmd.Flags().StringVar(&channelPublicURL, "public-url", "", "public URL for the agent card (defaults to http://<listen>/)")
	channelCmd.Flags().StringVar(&channelAuthToken, "auth-token", "", "Bearer token for authentication (empty = no auth)")
	rootCmd.AddCommand(channelCmd)
}
