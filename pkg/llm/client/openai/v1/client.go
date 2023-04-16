package v1

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"friday/pkg/llm"
)

type OpenAIV1 struct {
	baseUri string
	key     string
}

func NewOpenAIV1() *OpenAIV1 {
	key := os.Getenv("OPENAI_KEY")
	return &OpenAIV1{
		baseUri: "https://api.openai.com/v1",
		key:     key,
	}
}

var _ llm.LLM = &OpenAIV1{}

func (o *OpenAIV1) request(path string, method string, body io.Reader) ([]byte, error) {
	uri, err := url.JoinPath(o.baseUri, path)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(method, uri, body)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", o.key))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Read Response Body
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("fail to call openai, status code error: %d", resp.StatusCode)
	}
	return respBody, nil
}
