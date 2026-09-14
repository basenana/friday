package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/basenana/friday/actor"
	fridaydaemon "github.com/basenana/friday/daemon"
)

var daemonPort int

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Start Friday's local WebSocket daemon",
	Long:  "Start the local Friday daemon. It listens only on 127.0.0.1 and exposes a WebSocket API at /ws.",
	RunE: func(cmd *cobra.Command, args []string) error {
		catalog, err := fridaydaemon.NewSessionCatalog(sessMgr)
		if err != nil {
			return err
		}
		registryConfig := actor.DefaultRegistryConfig()
		registryConfig.Catalog = sessMgr
		registryConfig.AgentPlanEntry = true
		registry := actor.NewRegistry(sessMgr, cfg, registryConfig)
		defer registry.ShutdownAll()

		server, err := fridaydaemon.NewServer(fridaydaemon.Config{Port: daemonPort}, registry, catalog)
		if err != nil {
			registry.ShutdownAll()
			return err
		}

		signalCtx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		go func() {
			<-signalCtx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := server.Shutdown(shutdownCtx); err != nil {
				fmt.Fprintln(os.Stderr, "daemon shutdown error:", err)
			}
		}()
		return server.Start()
	},
}

func init() {
	daemonCmd.Flags().IntVar(&daemonPort, "port", 8999, "local WebSocket port")
	rootCmd.AddCommand(daemonCmd)
}
