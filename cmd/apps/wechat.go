package apps

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"friday/config"
	"friday/pkg/friday"
	"friday/pkg/llm/prompts"
	"friday/pkg/utils/logger"
)

var WeChatCmd = &cobra.Command{
	Use:   "chat",
	Short: "conclusion base on chat",
	Run: func(cmd *cobra.Command, args []string) {
		ps := fmt.Sprint(strings.Join(args, " "))
		loader := config.NewConfigLoader()
		cfg, err := loader.GetConfig()
		if err != nil {
			panic(err)
		}

		if cfg.Debug {
			logger.SetDebug(cfg.Debug)
		}
		if err := chat(&cfg, ps); err != nil {
			panic(err)
		}
	},
}

func chat(config *config.Config, ps string) error {
	f, err := friday.NewFriday(config)
	if err != nil {
		return err
	}
	p := prompts.NewWeChatPrompt()
	a, err := f.ChatConclusionFromFile(p, ps)
	if err != nil {
		return err
	}
	fmt.Println("Answer: ")
	fmt.Println(a)
	return nil
}
