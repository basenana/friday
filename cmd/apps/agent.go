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
	"github.com/spf13/cobra"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/pkg/build/common"
	"github.com/basenana/friday/pkg/friday"
	"github.com/basenana/friday/pkg/utils/logger"
)

var AgentCmd = &cobra.Command{
	Use:   "agent",
	Short: "agent",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		var agentLog = logger.NewLog("agent")
		// init friday
		loader := config.NewConfigLoader()
		cfg, err := loader.GetConfig()
		if err != nil {
			agentLog.Panicw("load config error", "error", err)
		}

		friday.Fri, err = common.NewFriday(&cfg)
		if err != nil {
			agentLog.Errorf("init friday error: %v", err)
		}
	},
}

func init() {
	AgentCmd.AddCommand(QuestionCmd)
	AgentCmd.AddCommand(ChatCmd)
	AgentCmd.AddCommand(IngestCmd)
	AgentCmd.AddCommand(WeChatCmd)
	AgentCmd.AddCommand(SummaryCmd)
	AgentCmd.AddCommand(KeywordsCmd)
}
