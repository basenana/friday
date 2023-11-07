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

	"github.com/basenana/friday/pkg/friday"
)

var WeChatCmd = &cobra.Command{
	Use:   "chat",
	Short: "conclusion base on chat",
	Run: func(cmd *cobra.Command, args []string) {
		ps := fmt.Sprint(strings.Join(args, " "))

		if err := chat(ps); err != nil {
			panic(err)
		}
	},
}

func chat(ps string) error {
	a, err := friday.Fri.ChatConclusionFromFile(ps)
	if err != nil {
		return err
	}
	fmt.Println("Answer: ")
	fmt.Println(a)
	return nil
}
