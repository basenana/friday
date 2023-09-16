package main

import (
	"path"

	"github.com/spf13/cobra"

	"friday/cmd/apps"
	"friday/config"
)

var RootCmd = &cobra.Command{
	Use:   "friday",
	Short: "friday",
	Run: func(cmd *cobra.Command, args []string) {
		_ = cmd.Help()
	},
}

func init() {
	RootCmd.AddCommand(apps.QuestionCmd)
	RootCmd.AddCommand(apps.IngestCmd)
	RootCmd.AddCommand(apps.WeChatCmd)
	RootCmd.PersistentFlags().StringVar(&config.FilePath, "config", path.Join(config.LocalUserPath(), config.DefaultConfigBase), "friday config file")
}

func main() {
	if err := RootCmd.Execute(); err != nil {
		panic(err)
	}
}
