/*
 Copyright 2024 Friday Author.

 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package apps

import (
	"sync"

	"github.com/spf13/cobra"

	"github.com/basenana/friday/api"
	"github.com/basenana/friday/config"
	"github.com/basenana/friday/pkg/dispatch"
	"github.com/basenana/friday/pkg/service"
	"github.com/basenana/friday/pkg/utils"
	"github.com/basenana/friday/pkg/utils/logger"
)

var ServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "serve",
	Run: func(cmd *cobra.Command, args []string) {
		loader := config.NewConfigLoader()
		cfg, err := loader.GetConfig()
		if err != nil {
			panic(err)
		}

		if cfg.Debug {
			logger.SetDebug(true)
		}
		service.ChainPool = dispatch.NewPool(cfg.PoolNum)

		stop := utils.HandleTerminalSignal()
		wg := sync.WaitGroup{}
		s, err := api.NewHttpServer(cfg)
		if err != nil {
			panic(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Run(stop)
		}()
		wg.Wait()
	},
}
