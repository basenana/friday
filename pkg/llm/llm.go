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

package llm

import (
	"context"

	"github.com/basenana/friday/pkg/llm/prompts"
)

type LLM interface {
	// Completion chat with llm just once
	Completion(ctx context.Context, prompt prompts.PromptTemplate, parameters map[string]string) (answers []string, tokens map[string]int, err error)
	/*
		Chat: chat with llm with history.
		 history example: [
			  {"user": "Hello robot!"}
			  {"assistant": "Hello"}
			  {"user": "When is today?"}
			  {"assistant": "Today is Monday"}
		 ]
	*/
	Chat(ctx context.Context, history []map[string]string, prompt prompts.PromptTemplate, parameters map[string]string) (answers []string, tokens map[string]int, err error)
}
