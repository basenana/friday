package apps

import (
	"github.com/spf13/cobra"

	"friday/config"
	"friday/pkg/friday"
)

var IngestCmd = &cobra.Command{
	Use:   "ingest",
	Short: "ingest knowledge",
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			panic("ingest path is needed")
		}
		ps := args[0]
		loader := config.NewConfigLoader()
		cfg, err := loader.GetConfig()
		if err != nil {
			panic(err)
		}

		if err := ingest(&cfg, ps); err != nil {
			panic(err)
		}
	},
}

func ingest(config *config.Config, ps string) error {
	f, err := friday.NewFriday(config)
	if err != nil {
		return err
	}
	err = f.IngestFromElementFile(ps)
	if err != nil {
		return err
	}
	return nil
}
