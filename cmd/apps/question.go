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
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/basenana/friday/pkg/friday"
)

var QuestionCmd = &cobra.Command{
	Use:   "question",
	Short: "question base on knowledge",
	Run: func(cmd *cobra.Command, args []string) {
		question := fmt.Sprint(strings.Join(args, " "))

		if err := run(question); err != nil {
			panic(err)
		}
	},
}

func run(question string) error {
	f := friday.Fri.WithContext(context.TODO()).Question(question)
	res := &friday.ChatState{}
	f = f.Complete(res)
	if f.Error != nil {
		return f.Error
	}

	fmt.Println("Answer: ")
	fmt.Println(res.Answer)
	fmt.Printf("Usage: %v", res.Tokens)
	return nil
}
