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
	"context"
	"fmt"
	"strings"

	"github.com/basenana/friday/pkg/llm/prompts"
	"github.com/basenana/friday/pkg/models"
)

func (f *Friday) ChatWithDir(
	ctx context.Context,
	dirId int64,
	history []map[string]string,
) (dialogues []map[string]string, tokens map[string]int, err error) {
	// search for docs
	questions := ""
	for _, d := range history {
		if d["role"] == "user" {
			questions = fmt.Sprintf("%s\n%s", questions, d["content"])
		}
	}
	c, err := f.searchDocs(ctx, models.DocQuery{ParentId: dirId}, questions)
	if err != nil {
		return nil, nil, err
	}

	return f.chatWithInfo(ctx, history, c)
}

func (f *Friday) ChatWithDoc(
	ctx context.Context,
	oid int64,
	history []map[string]string,
) (dialogues []map[string]string, tokens map[string]int, err error) {
	// search for docs
	questions := ""
	for _, d := range history {
		if d["role"] == "user" {
			questions = fmt.Sprintf("%s\n%s", questions, d["content"])
		}
	}
	c, err := f.searchDocs(ctx, models.DocQuery{ParentId: oid}, questions)
	if err != nil {
		return nil, nil, err
	}

	return f.chatWithInfo(ctx, history, c)
}

func (f *Friday) chatWithInfo(ctx context.Context, history []map[string]string, info string) (dialogues []map[string]string, tokens map[string]int, err error) {
	if history[0]["role"] == "system" {
		history = history[1:]
	}
	history = append([]map[string]string{{
		"role":    "system",
		"content": fmt.Sprintf("基于以下已知信息，简洁和专业的来回答用户的问题。答案请使用中文。 \n\n已知内容: %s", info),
	}}, history...)

	dialogues, tokens, err = f.chat(ctx, history)
	return
}

func (f *Friday) chat(ctx context.Context, history []map[string]string) (dialogues []map[string]string, tokens map[string]int, err error) {
	tokens = make(map[string]int)
	dialogues = make([]map[string]string, 0, len(history)+1)
	copy(dialogues, history)

	// If the number of dialogue rounds exceeds 2 rounds, should conclude it.
	if len(history) >= 4 {
		sumDialogue := make([]map[string]string, 0, len(history))
		copy(sumDialogue, history)
		sumDialogue = append(sumDialogue, map[string]string{
			"role":    "system",
			"content": "简要总结一下对话内容，用作后续的上下文提示 prompt，控制在 200 字以内",
		})
		sum, usage, e := f.LLM.Chat(ctx, sumDialogue)
		if e != nil {
			err = e
			return
		}
		tokens = mergeTokens(usage, tokens)

		// add context prompt for dialogue
		dialogues[0] = map[string]string{
			"role":    "system",
			"content": fmt.Sprintf("这是历史聊天总结作为前情提要：%s", sum["content"]),
		}
		dialogues = append(dialogues, history[len(history)-5:len(history)-1]...)
	}

	// go for llm
	ans, usage, err := f.LLM.Chat(ctx, dialogues)
	if err != nil {
		return
	}
	f.Log.Debugf("Chat result: %s", ans)
	dialogues = append(dialogues, ans)
	tokens = mergeTokens(tokens, usage)
	return
}

func (f *Friday) Question(ctx context.Context, query models.DocQuery, q string) (string, map[string]int, error) {
	prompt := prompts.NewQuestionPrompt(f.Prompts[questionPromptKey])
	c, err := f.searchDocs(ctx, query, q)
	if err != nil {
		return "", nil, err
	}
	if f.LLM != nil {
		ans, usage, err := f.LLM.Completion(ctx, prompt, map[string]string{
			"context":  c,
			"question": q,
		})
		if err != nil {
			return "", nil, fmt.Errorf("llm completion error: %w", err)
		}
		f.Log.Debugf("Question result: %s", ans[0])
		return ans[0], usage, nil
	}
	return c, nil, nil
}

func (f *Friday) searchDocs(ctx context.Context, query models.DocQuery, q string) (string, error) {
	f.Log.Debugf("vector query for %s ...", q)
	qv, _, err := f.Embedding.VectorQuery(ctx, q)
	if err != nil {
		return "", fmt.Errorf("vector embedding error: %w", err)
	}
	docs, err := f.Vector.Search(ctx, query, qv, *f.VectorTopK)
	if err != nil {
		return "", fmt.Errorf("vector search error: %w", err)
	}

	cs := []string{}
	for _, c := range docs {
		f.Log.Debugf("searched from [%s] for %s", c.Name, c.Content)
		cs = append(cs, c.Content)
	}
	return strings.Join(cs, "\n"), nil
}

func mergeTokens(tokens, merged map[string]int) map[string]int {
	result := make(map[string]int)
	for k, v := range tokens {
		result[k] = v
	}
	for k, v := range merged {
		if _, ok := result[k]; !ok {
			result[k] = v
		} else {
			result[k] += v
		}
	}
	return result
}
