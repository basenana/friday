package v1

import (
	"bytes"
	"encoding/json"

	"friday/pkg/llm/prompts"
)

type ChatResult struct {
	Id      string         `json:"id"`
	Object  string         `json:"object"`
	Created int            `json:"created"`
	Model   string         `json:"model"`
	Choices []ChatChoice   `json:"choices"`
	Usage   map[string]int `json:"usage"`
}

type ChatChoice struct {
	Index        int               `json:"index"`
	Message      map[string]string `json:"message"`
	FinishReason string            `json:"finish_reason"`
}

func (o *OpenAIV1) Chat(prompt prompts.PromptTemplate, parameters map[string]string) ([]string, error) {
	path := "chat/completions"

	model := "gpt-3.5-turbo"
	p, err := prompt.String(parameters)
	if err != nil {
		return nil, err
	}
	data := map[string]interface{}{
		"model":             model,
		"messages":          []interface{}{map[string]string{"role": "user", "content": p}},
		"max_tokens":        1024,
		"temperature":       0.7,
		"top_p":             1,
		"frequency_penalty": 0,
		"presence_penalty":  0,
		"n":                 1,
	}
	postBody, _ := json.Marshal(data)

	respBody, err := o.request(path, "POST", bytes.NewBuffer(postBody))
	if err != nil {
		return nil, err
	}

	var res ChatResult
	err = json.Unmarshal(respBody, &res)
	if err != nil {
		return nil, err
	}
	ans := make([]string, len(res.Choices))
	for i, c := range res.Choices {
		ans[i] = c.Message["content"]
	}
	return ans, err
}
