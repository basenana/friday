package v1

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"go.uber.org/zap"

	"friday/pkg/llm"
	"friday/pkg/utils/logger"
)

type OpenAIV1 struct {
	log     *zap.SugaredLogger
	baseUri string
	key     string
}

func NewOpenAIV1() *OpenAIV1 {
	key := os.Getenv("OPENAI_KEY")
	return &OpenAIV1{
		log:     logger.NewLogger("openai"),
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

	o.log.Debugf("request: %s", uri)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Read Response Body
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("fail to call openai, status code error: %d, resp body: %s", resp.StatusCode, string(respBody))
	}
	//o.log.Debugf("response: %s", respBody)
	return respBody, nil
}
