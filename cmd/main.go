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

package main

import (
	"github.com/spf13/cobra"

	"github.com/basenana/friday/cmd/apps"
	"github.com/basenana/friday/config"
	"github.com/basenana/friday/pkg/build/common"
	"github.com/basenana/friday/pkg/friday"
)

var RootCmd = &cobra.Command{
	Use:   "friday",
	Short: "friday",
	Run: func(cmd *cobra.Command, args []string) {
		_ = cmd.Help()
	},
}

func init() {
	// init friday
	loader := config.NewConfigLoader()
	cfg, err := loader.GetConfig()
	if err != nil {
		panic(err)
	}

	friday.Fri, err = common.NewFriday(&cfg)
	if err != nil {
		panic(err)
	}

	RootCmd.AddCommand(apps.QuestionCmd)
	RootCmd.AddCommand(apps.ChatCmd)
	RootCmd.AddCommand(apps.IngestCmd)
	RootCmd.AddCommand(apps.WeChatCmd)
	RootCmd.AddCommand(apps.SummaryCmd)
	RootCmd.AddCommand(apps.KeywordsCmd)
}

func main() {
	if err := RootCmd.Execute(); err != nil {
		panic(err)
	}
}
