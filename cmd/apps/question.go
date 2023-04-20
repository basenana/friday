package apps

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"friday/config"
	"friday/pkg/friday"
	"friday/pkg/llm/prompts"
)

var QuestionCmd = &cobra.Command{
	Use:   "question",
	Short: "question base on knowledge",
	Run: func(cmd *cobra.Command, args []string) {
		question := fmt.Sprint(strings.Join(args, " "))
		loader := config.NewConfigLoader()
		cfg, err := loader.GetConfig()
		if err != nil {
			panic(err)
		}

		if err := run(&cfg, question); err != nil {
			panic(err)
		}
	},
}

func run(config *config.Config, question string) error {
	f, err := friday.NewFriday(config)
	if err != nil {
		return err
	}
	p := prompts.NewQuestionPrompt()
	a, err := f.Question(p, question)
	if err != nil {
		return err
	}
	fmt.Println(a)
	return nil
}
