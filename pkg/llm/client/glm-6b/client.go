package glm_6b

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"friday/pkg/llm"
	"friday/pkg/llm/prompts"
)

type GLM struct {
	baseUri string
}

func NewGLM(uri string) llm.LLM {
	return &GLM{
		baseUri: uri,
	}
}

var _ llm.LLM = &GLM{}

func (o *GLM) request(path string, method string, body io.Reader) ([]byte, error) {
	uri, err := url.JoinPath(o.baseUri, path)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(method, uri, body)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Read Response Body
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("fail to call glm-6b, status code error: %d", resp.StatusCode)
	}
	return respBody, nil
}

type CompletionResult struct {
	Response string     `json:"response"`
	History  [][]string `json:"history"`
	Status   int        `json:"status"`
	Time     string     `json:"time"`
}

func (o *GLM) Completion(prompt prompts.PromptTemplate, parameters map[string]string) ([]string, error) {
	path := ""

	p, err := prompt.String(parameters)
	if err != nil {
		return nil, err
	}
	data := map[string]interface{}{
		"prompt":      p,
		"max_length":  10240,
		"temperature": 0.7,
		"top_p":       1,
	}
	postBody, _ := json.Marshal(data)

	respBody, err := o.request(path, "POST", bytes.NewBuffer(postBody))
	if err != nil {
		return nil, err
	}

	var res CompletionResult
	err = json.Unmarshal(respBody, &res)
	if err != nil {
		return nil, err
	}
	ans := []string{res.Response}
	return ans, err
}
