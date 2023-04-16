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

package friday

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"friday/pkg/llm/prompts"
	"friday/pkg/models"
	"friday/pkg/utils/files"
)

func (f *Friday) ChatConclusion(prompt prompts.PromptTemplate, chat string) (string, error) {
	if f.llm != nil {
		ans, err := f.llm.Chat(prompt, map[string]string{
			"context": chat,
		})
		if err != nil {
			return "", fmt.Errorf("llm completion error: %w", err)
		}
		return ans[0], nil
	}
	return "", nil
}

func (f *Friday) ChatConclusionFromElementFile(prompt prompts.PromptTemplate, chatFile string) (string, error) {
	var ans []string
	doc, err := os.ReadFile(chatFile)
	if err != nil {
		return "", err
	}
	elements := []models.Element{}
	if err = json.Unmarshal(doc, &elements); err != nil {
		return "", err
	}
	merged := f.spliter.Merge(elements)
	for _, m := range merged {
		a, err := f.ChatConclusion(prompt, m.Content)
		if err != nil {
			return "", err
		}
		ans = append(ans, a)
	}
	return strings.Join(ans, "\n=============\n"), nil
}

func (f *Friday) ChatConclusionFromFile(prompt prompts.PromptTemplate, chatFile string) (string, error) {
	fs, err := files.ReadFiles(chatFile)
	if err != nil {
		return "", err
	}

	elements := []models.Element{}
	for n, file := range fs {
		subDocs := f.spliter.Split(file)
		for i, subDoc := range subDocs {
			e := models.Element{
				Content: subDoc,
				Metadata: models.Metadata{
					Source: n,
					Group:  strconv.Itoa(i),
				},
			}
			elements = append(elements, e)
		}
	}

	var ans []string
	for _, m := range elements {
		a, err := f.ChatConclusion(prompt, m.Content)
		if err != nil {
			return "", err
		}
		ans = append(ans, a)
	}
	return strings.Join(ans, "\n=============\n"), nil
}
