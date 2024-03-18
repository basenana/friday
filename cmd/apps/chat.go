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
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/basenana/friday/pkg/friday"
	"github.com/basenana/friday/pkg/models"
)

var ChatCmd = &cobra.Command{
	Use:   "chat",
	Short: "chat with llm base on knowledge",
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) <= 1 {
			panic("dirId and history is needed.")
		}
		dirIdStr := args[0]
		dirId, err := strconv.Atoi(dirIdStr)
		if err != nil {
			panic(err)
		}

		historyStr := fmt.Sprint(strings.Join(args[1:], " "))

		history := make([]map[string]string, 0)
		err = json.Unmarshal([]byte(historyStr), &history)
		if err != nil {
			panic(err)
		}

		if err := chat(int64(dirId), history); err != nil {
			panic(err)
		}
	},
}

func chat(dirId int64, history []map[string]string) error {
	f := friday.Fri.WithContext(context.TODO()).History(history).SearchIn(&models.DocQuery{
		ParentId: dirId,
	})
	resp := make(chan map[string]string)
	res := &friday.ChatState{
		Response: resp,
	}
	go func() {
		f = f.Chat(res)
		close(resp)
	}()
	if f.Error != nil {
		return f.Error
	}

	fmt.Println("Dialogues: ")
	for line := range res.Response {
		fmt.Printf("%v: %v\n", time.Now().Format("15:04:05"), line)
	}
	return nil
}
