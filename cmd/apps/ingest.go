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
	"github.com/spf13/cobra"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/pkg/friday"
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
