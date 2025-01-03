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
	"path"

	"github.com/spf13/cobra"

	"github.com/basenana/friday/cmd/apps"
	"github.com/basenana/friday/config"
	"github.com/basenana/friday/pkg/utils/logger"
)

var RootCmd = &cobra.Command{
	Use:   "friday",
	Short: "friday",
	Run: func(cmd *cobra.Command, args []string) {
		_ = cmd.Help()
	},
}

func init() {
	RootCmd.AddCommand(apps.ServeCmd)
	RootCmd.AddCommand(apps.AgentCmd)
	RootCmd.PersistentFlags().StringVar(&config.FilePath, "config", path.Join(config.LocalUserPath(), config.DefaultConfigBase), "friday config file")
}

func main() {
	logger.InitLog()
	defer logger.Sync()
	if err := RootCmd.Execute(); err != nil {
		panic(err)
	}
}
