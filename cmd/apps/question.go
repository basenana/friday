/*
 * Copyright 2023 friday
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package apps

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/pkg/friday"
	"github.com/basenana/friday/pkg/llm/prompts"
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
	fmt.Println("Answer: ")
	fmt.Println(a)
	return nil
}
