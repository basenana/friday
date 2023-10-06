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

	"github.com/spf13/cobra"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/pkg/friday"
	fridaysummary "github.com/basenana/friday/pkg/friday/summary"
)

var (
	summaryType string
)

var SummaryCmd = &cobra.Command{
	Use:   "summary",
	Short: "Summarize an article in short words",
	Run: func(cmd *cobra.Command, args []string) {
		ps := args[0]
		loader := config.NewConfigLoader()
		cfg, err := loader.GetConfig()
		if err != nil {
			panic(err)
		}

		if err := summary(&cfg, ps); err != nil {
			panic(err)
		}
	},
}

func init() {
	SummaryCmd.Flags().StringVar(&summaryType, "type", "MapReduce", "type of summary")
}

func summary(config *config.Config, ps string) error {
	f, err := friday.NewFriday(config)
	if err != nil {
		return err
	}
	a, err := f.SummaryFromOriginFile(ps, fridaysummary.SummaryType(summaryType))
	if err != nil {
		return err
	}
	fmt.Println("Answer: ")
	fmt.Println(a)
	return nil
}